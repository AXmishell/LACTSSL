import axios, { type AxiosRequestConfig } from 'axios'
import type { ApiError } from './types'
import { currentLocale, t } from './i18n'

declare global {
  interface Window {
    __LACTSSL_ENTRY__?: string
    __LACTSSL_API_BASE__?: string
  }
}

// 服务端在 index.html 中注入当前安全入口；裸页面（/dashboard）也依赖它拼接口地址。
export const appEntry = window.__LACTSSL_ENTRY__ || ''
export const apiBase = window.__LACTSSL_API_BASE__ || (appEntry ? `/${appEntry}/api` : '/api')
// 登录、安装和重置使用裸路径；安全入口只在首次访问时写入 HttpOnly 门禁 Cookie。
export const loginPath = '/login'
export const installPath = '/install'
export const resetPath = '/reset'

const csrfHeader = 'X-LACTSSL-CSRF'

// 双提交 CSRF：服务端下发非 HttpOnly 的 lactssl_csrf Cookie，前端把它放进请求头。
// 旧会话尚未拿到 Cookie 时发送占位值，服务端会退回兼容校验。
function csrfToken(): string {
  // HTTPS 下服务端使用 __Host- 前缀，需同时兼容明文 HTTP 的旧名称。
  const match = document.cookie.match(/(?:^|;\s*)(?:__Host-)?lactssl_csrf=([^;]*)/)
  return match ? decodeURIComponent(match[1]) : '1'
}

// 旧版本把 JWT 存在 localStorage，切换为 HttpOnly Cookie 后顺手清理。
localStorage.removeItem('lactssl.token')

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
    // 代理或安全入口配置错误时可能返回 index.html，先校验响应类型，
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
    const message = data?.message || (error.code === 'ECONNABORTED' ? t('api.timeout') : t('api.networkError'))
    const url = error.config?.url || ''
    // /auth/me 是登录状态探测，401 属于正常结果，不应触发跳转。
    if (error.response?.status === 401 && !url.includes('/auth/login') && !url.includes('/auth/me') && !url.includes('/auth/reauth')) {
      window.dispatchEvent(new Event('lactssl:unauthorized'))
      if (window.location.pathname !== loginPath) {
        window.location.assign(loginPath)
      }
    }
    const err = new Error(message) as ApiError
    err.data = data
    err.status = error.response?.status
    err.totpRequired = !!data?.totp_required
    err.reauthRequired = !!data?.reauth_required
    err.secondaryVerifyRequired = !!data?.secondary_verify_required
    return Promise.reject(err)
  }
)

function request<T>(config: AxiosRequestConfig): Promise<T> {
  return http.request(config) as unknown as Promise<T>
}

export const api = {
  get: <T = any>(url: string, params?: Record<string, any>, headers?: Record<string, string>) =>
    request<T>({ method: 'GET', url, params, headers }),
  post: <T = any>(url: string, data?: any, headers?: Record<string, string>) =>
    request<T>({ method: 'POST', url, data, headers }),
  put: <T = any>(url: string, data?: any, headers?: Record<string, string>) =>
    request<T>({ method: 'PUT', url, data, headers }),
  delete: <T = any>(url: string, headers?: Record<string, string>) => request<T>({ method: 'DELETE', url, headers })
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
    let reauthRequired = false
    let totpRequired = false
    let secondaryVerifyRequired = false
    try {
      const body = await response.json()
      if (body?.message) message = body.message
      reauthRequired = !!body?.reauth_required
      totpRequired = !!body?.totp_required
      secondaryVerifyRequired = !!body?.secondary_verify_required
    } catch (_) {
      // 忽略非 JSON 响应
    }
    const error = new Error(message) as ApiError
    error.reauthRequired = reauthRequired
    error.totpRequired = totpRequired
    error.secondaryVerifyRequired = secondaryVerifyRequired
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
