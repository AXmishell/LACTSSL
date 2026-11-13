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
  private_ca_key_export: 'secondaryVerify.action.privateCaKeyExport'
}

export interface SecondaryVerifyState {
  visible: boolean
  action: SecondaryVerifyAction
  mode: VerifyMode
  password: string
  totp: string
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

// currentVerifyMode 与后端 effectiveVerifyMode 保持一致：
// 需要 TOTP 的模式在 TOTP 未启用时回退到密码。
export async function currentVerifyMode(): Promise<VerifyMode> {
  const [settings] = await Promise.all([loadSettings(true), loadUser()])
  const mode = settings.verify_mode
  if (mode === 'none') return 'none'
  if ((mode === 'totp' || mode === 'password_totp') && state.user?.totp_enabled) {
    return mode
  }
  return 'password'
}

// verifySecondary 直接使用调用方已经收集的凭证换取二次验证 token。
// 适用于恢复备份、修改安全入口这类已经有内联验证字段的表单。
export async function verifySecondary(
  action: SecondaryVerifyAction,
  password: string,
  totp: string
): Promise<string> {
  const data = await api.post<{ token: string; expires_in: number }>('/auth/secondary-verify', {
    action,
    password,
    totp
  })
  return data.token
}

// requestSecondaryVerification 打开独立的“二次验证”对话框，成功后返回 token。
// 用户取消时返回 null，调用方应直接终止当前操作。
export async function requestSecondaryVerification(
  action: SecondaryVerifyAction
): Promise<string | null> {
  const mode = await currentVerifyMode()
  if (secondaryVerifyState.visible) {
    return null
  }
  secondaryVerifyState.action = action
  secondaryVerifyState.mode = mode
  secondaryVerifyState.password = ''
  secondaryVerifyState.totp = ''
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
  secondaryVerifyState.resolve = null
  secondaryVerifyState.visible = false
  resolve?.(token)
}

// 会话失效时关闭验证框，避免登录页上方残留一个无法提交的弹窗。
window.addEventListener('lactssl:unauthorized', () => {
  if (secondaryVerifyState.visible) {
    finishSecondaryVerify(null)
  }
})
