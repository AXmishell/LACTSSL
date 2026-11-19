import axios, { type AxiosRequestConfig } from 'axios'
import type { ApiError } from './types'
import { currentLocale, t } from './i18n'
import { removeStorage } from './storage'

declare global {
  interface Window {
    __LACTSSL_API_BASE__?: string
  }
}

// 服务端在 index.html 中注入接口前缀；接口固定挂在裸 /api 下，与门禁入口无关。
export const apiBase = window.__LACTSSL_API_BASE__ || '/api'
// 登录、安装和重置使用裸路径；门禁入口只在首次访问时写入 HttpOnly 门禁 Cookie。
export const loginPath = '/login'
export const installPath = '/install'
export const resetPath = '/reset'

const csrfHeader = 'X-LACTSSL-CSRF'

// 读取单个 Cookie；同名的 __Host- 与非前缀 Cookie 可能同时存在。
function readCookie(name: string): string | null {
  const match = document.cookie.match(new RegExp('(?:^|;\\s*)' + name + '=([^;]*)'))
  return match ? decodeURIComponent(match[1]) : null
}

// 双提交 CSRF：服务端下发非 HttpOnly 的 lactssl_csrf Cookie，前端把它放进请求头。
// 旧会话尚未拿到 Cookie 时发送占位值，服务端会退回兼容校验。
function csrfToken(): string {
  // 必须优先 __Host-：从明文切换到 HTTPS（或升级旧版本）后，浏览器里可能同时
  // 保留旧的 lactssl_csrf 和新的 __Host-lactssl_csrf。document.cookie 的顺序
  // 由浏览器决定，若先匹配到旧值，服务端却优先校验 __Host-，所有写操作都会
  // 被当成 CSRF 失败并提示“请先登录”。
  return readCookie('__Host-lactssl_csrf') ?? readCookie('lactssl_csrf') ?? '1'
}

// 旧版本把 JWT 存在 localStorage，切换为 HttpOnly Cookie 后顺手清理。
removeStorage('lactssl.token')

const http = axios.create({
  baseURL: apiBase,
  timeout: 180000,
  withCredentials: true
})

http.interceptors.request.use((config) => {
  // Cookie 认证依赖自定义头抵御 CSRF；同源请求不会触发预检。
  config.headers[csrfHeader] = csrfToken()
  // 让后端按当前界面语言返回错误消息、动态元数据与持久化日志。
  config.headers['X-LACTSSL-Language'] = currentLocale()
  return config
})

http.interceptors.response.use(
  (response) => {
    // 代理或门禁入口配置错误时可能返回 index.html，先校验响应类型，
    // 避免把 HTML 当成业务数据（例如误判系统初始化状态）。
    const contentType = String(response.headers['content-type'] || '')
    if (response.status !== 204 && response.data !== '' && !contentType.includes('application/json')) {
      throw new Error(t('api.nonJson'))
    }
    const body = response.data
    // 后端统一返回 { success, data }，这里直接解包业务数据。
    if (body && typeof body === 'object' && Object.prototype.hasOwnProperty.call(body, 'success')) {
      return body.data
    }
    return body
  },
  (error: any) => {
    const data = error.response?.data
    return Promise.reject(makeApiError(data, error, undefined))
  }
)

// makeApiError 把 axios 错误转换为统一的 ApiError，供拦截器与重试逻辑复用。
function makeApiError(data: any, error: any, fallback?: string, overrideMessage = false): ApiError {
  const url = error.config?.url || ''
  const message = overrideMessage
    ? fallback || t('api.networkError')
    : data?.message || fallback || (error.code === 'ECONNABORTED' ? t('api.timeout') : t('api.networkError'))
  // /auth/me 是登录状态探测，401 属于正常结果，不应触发跳转。
  if (error.response?.status === 401 && !url.includes('/auth/login') && !url.includes('/auth/me')) {
    window.dispatchEvent(new Event('lactssl:unauthorized'))
    if (window.location.pathname !== loginPath) {
      window.location.assign(loginPath)
    }
  }
  const err = new Error(message) as ApiError
  err.data = data
  err.status = error.response?.status
  err.totpRequired = !!data?.totp_required
  err.passkeyRequired = !!data?.passkey_required
  return err
}

function request<T>(config: AxiosRequestConfig): Promise<T> {
  return http.request(config) as unknown as Promise<T>
}

export const api = {
  get: <T = any>(url: string, params?: Record<string, any>, headers?: Record<string, string>) =>
    request<T>({ method: 'GET', url, params, headers }),
  post: <T = any>(url: string, data?: any, headers?: Record<string, string>) =>
    request<T>({ method: 'POST', url, data, headers }),
  // 下载并安装程序发布包可能持续数分钟，单独放宽超时（服务端写超时为 10 分钟）。
  postSlow: <T = any>(url: string, data?: any, headers?: Record<string, string>) =>
    request<T>({ method: 'POST', url, data, headers, timeout: 540000 }),
  put: <T = any>(url: string, data?: any, headers?: Record<string, string>) =>
    request<T>({ method: 'PUT', url, data, headers }),
  delete: <T = any>(url: string, headers?: Record<string, string>) => request<T>({ method: 'DELETE', url, headers }),
  deleteWithBody: <T = any>(url: string, data?: any, headers?: Record<string, string>) =>
    request<T>({ method: 'DELETE', url, data, headers })
}

export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : t('common.operationFailed')
}

// 统一读取文件下载地址。会话在 HttpOnly Cookie 中，fetch 同源请求会自动携带。
export async function downloadFile(url: string, filename: string, init?: RequestInit): Promise<void> {
  const headers = new Headers(init?.headers)
  if (init?.method && init.method !== 'GET') {
    headers.set(csrfHeader, csrfToken())
  }
  headers.set('X-LACTSSL-Language', currentLocale())
  const response = await fetch(`${apiBase}${url}`, {
    credentials: 'same-origin',
    ...init,
    headers
  })
  if (!response.ok) {
    let message = t('api.downloadFailed')
    let totpRequired = false
    try {
      const body = await response.json()
      if (body?.message) message = body.message
      totpRequired = !!body?.totp_required
    } catch (_) {
      // 忽略非 JSON 响应
    }
    const error = new Error(message) as ApiError
    error.totpRequired = totpRequired
    throw error
  }
  const blob = await response.blob()
  const link = document.createElement('a')
  link.href = URL.createObjectURL(blob)
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(link.href)
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch (_) {
    const area = document.createElement('textarea')
    area.value = text
    document.body.appendChild(area)
    area.select()
    const ok = document.execCommand('copy')
    area.remove()
    return ok
  }
}
