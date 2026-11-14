import { reactive } from 'vue'
import { api } from './api'
import { loadSettings, loadUser, state } from './store'
import type { SecondaryVerifyAction, VerifyMode } from './types'

// 二次验证 token 放在自定义请求头中，业务接口据此确认身份验证已完成。
export const secondaryVerifyHeader = 'X-LACTSSL-VERIFY'

export const secondaryActionLabelKeys: Record<SecondaryVerifyAction, string> = {
  backup_download: 'secondaryVerify.action.backupDownload',
  backup_restore: 'secondaryVerify.action.backupRestore',
  entry_change: 'secondaryVerify.action.entryChange',
  private_ca_key_export: 'secondaryVerify.action.privateCaKeyExport',
  certificate_key_export: 'secondaryVerify.action.certificateKeyExport',
  settings_write: 'secondaryVerify.action.settingsWrite',
  account_write: 'secondaryVerify.action.accountWrite',
  task_write: 'secondaryVerify.action.taskWrite',
  deploy_execute: 'secondaryVerify.action.deployExecute',
  acme_write: 'secondaryVerify.action.acmeWrite',
  private_ca_write: 'secondaryVerify.action.privateCaWrite',
  certificate_write: 'secondaryVerify.action.certificateWrite',
  monitor_write: 'secondaryVerify.action.monitorWrite',
  notification_test: 'secondaryVerify.action.notificationTest',
  plugin_upload: 'secondaryVerify.action.pluginUpload'
}

export interface SecondaryVerifyState {
  visible: boolean
  action: SecondaryVerifyAction
  mode: VerifyMode
  password: string
  totp: string
  passkeyProof: string
  loading: boolean
  error: string
  resolve: ((token: string | null) => void) | null
}

export const secondaryVerifyState = reactive<SecondaryVerifyState>({
  visible: false,
  action: 'backup_download',
  mode: 'password',
  password: '',
  totp: '',
  passkeyProof: '',
  loading: false,
  error: '',
  resolve: null
})

export function verifyModeNeedsPassword(mode: VerifyMode): boolean {
  return mode === 'password' || mode === 'password_totp'
}

export function verifyModeNeedsTOTP(mode: VerifyMode): boolean {
  return mode === 'totp' || mode === 'password_totp'
}

export function verifyModeNeedsPasskey(mode: VerifyMode): boolean {
  return mode === 'passkey'
}

// currentVerifyMode 与后端 effectiveVerifyMode 保持一致：
// 需要 TOTP 或通行密钥的模式在对应验证方式未启用时回退到密码。
export async function currentVerifyMode(): Promise<VerifyMode> {
  const [settings] = await Promise.all([loadSettings(true), loadUser()])
  const mode = settings.verify_mode
  if (mode === 'none') return 'none'
  if ((mode === 'totp' || mode === 'password_totp') && state.user?.totp_enabled) {
    return mode
  }
  if (mode === 'passkey' && settings.passkey_enabled) {
    return mode
  }
  return 'password'
}

// verifySecondary 直接使用调用方已经收集的凭证换取二次验证 token。
// 适用于恢复备份、修改安全入口这类已经有内联验证字段的表单。
export async function verifySecondary(
  action: SecondaryVerifyAction,
  password: string,
  totp: string,
  passkeyProof = ''
): Promise<string> {
  const data = await api.post<{ token: string; expires_in: number }>('/auth/secondary-verify', {
    action,
    password,
    totp,
    passkey_proof: passkeyProof
  })
  return data.token
}

// requestSecondaryVerification 打开独立的“二次验证”对话框，成功后返回 token。
// 用户取消时返回 null，调用方应直接终止当前操作。
// 同一操作在 token 有效期内复用，避免批量操作重复弹窗。
const tokenCache = new Map<SecondaryVerifyAction, { token: string; expiresAt: number }>()
const TOKEN_CACHE_MS = 90_000

// stepUpRequired 与后端 step_up 开关保持一致；未返回该字段时默认要求验证。
// 通过通行密钥登录的会话已完成强认证，后端会直接放行，因此前端也不再弹窗。
export function stepUpRequired(action: SecondaryVerifyAction): boolean {
  if (state.user?.strong_auth) return false
  const map = state.settings?.step_up
  if (!map) return true
  if (!(action in map)) return true
  return map[action] !== false
}

export async function withSecondaryVerify<T>(
  action: SecondaryVerifyAction,
  run: (headers: Record<string, string>) => Promise<T>
): Promise<T | null> {
  if (!stepUpRequired(action)) {
    return run({})
  }
  const token = await requestSecondaryVerification(action)
  if (!token) return null
  return run({ [secondaryVerifyHeader]: token })
}

export async function requestSecondaryVerification(
  action: SecondaryVerifyAction
): Promise<string | null> {
  // 首次调用时先加载设置，避免在后端已关闭该操作时仍弹出验证框。
  if (!state.settings) {
    try {
      await loadSettings()
    } catch (_) {
      // 加载失败时按默认策略要求验证，避免意外绕过。
    }
  }
  // 返回空字符串表示该操作已关闭二次验证，调用方无需携带 token。
  if (!stepUpRequired(action)) {
    return ''
  }
  const cached = tokenCache.get(action)
  if (cached && cached.expiresAt > Date.now()) {
    return cached.token
  }
  const mode = await currentVerifyMode()
  if (secondaryVerifyState.visible) {
    return null
  }
  secondaryVerifyState.action = action
  secondaryVerifyState.mode = mode
  secondaryVerifyState.password = ''
  secondaryVerifyState.totp = ''
  secondaryVerifyState.passkeyProof = ''
  secondaryVerifyState.loading = false
  secondaryVerifyState.error = ''
  return new Promise((resolve) => {
    secondaryVerifyState.resolve = resolve
    secondaryVerifyState.visible = true
  })
}

// finishSecondaryVerify 由对话框调用，统一处理成功与取消。
export function finishSecondaryVerify(token: string | null): void {
  const resolve = secondaryVerifyState.resolve
  const action = secondaryVerifyState.action
  if (token) {
    tokenCache.set(action, { token, expiresAt: Date.now() + TOKEN_CACHE_MS })
  }
  secondaryVerifyState.resolve = null
  secondaryVerifyState.visible = false
  resolve?.(token)
}

// 会话失效时关闭验证框并清空 token 缓存，避免复用已失效的凭证。
window.addEventListener('lactssl:unauthorized', () => {
  tokenCache.clear()
  if (secondaryVerifyState.visible) {
    finishSecondaryVerify(null)
  }
})
