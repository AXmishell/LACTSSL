import { ElMessageBox } from 'element-plus'
import { api, downloadFile } from './api'
import { t } from './i18n'

// 高风险操作（导出私钥、下载备份）要求重新输入当前账户密码。
// 服务端返回 403 + reauth_required 后，这里提示用户完成验证并自动重试一次。
async function promptPassword(): Promise<string> {
  try {
    const result = await ElMessageBox.prompt(t('auth.reauthPasswordPrompt'), t('auth.reauthTitle'), {
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
      inputType: 'password',
      inputPlaceholder: t('auth.passwordPlaceholder')
    })
    return result.value
  } catch {
    throw new Error(t('auth.reauthCancelled'))
  }
}

async function promptTOTP(): Promise<string> {
  try {
    const result = await ElMessageBox.prompt(t('auth.reauthTotpPrompt'), t('auth.reauthTitle'), {
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
      inputPlaceholder: t('auth.totpPlaceholder')
    })
    return result.value
  } catch {
    throw new Error(t('auth.reauthCancelled'))
  }
}

export async function reauthenticate(): Promise<void> {
  const password = await promptPassword()
  try {
    await api.post('/auth/reauth', { password })
  } catch (error: any) {
    if (!error?.totpRequired) {
      throw error
    }
    const totp = await promptTOTP()
    await api.post('/auth/reauth', { password, totp })
  }
}

export async function withReauth<T>(task: () => Promise<T>): Promise<T> {
  try {
    return await task()
  } catch (error: any) {
    if (!error?.reauthRequired) {
      throw error
    }
    await reauthenticate()
    return await task()
  }
}

export function downloadFileWithReauth(url: string, filename: string, init?: RequestInit): Promise<void> {
  return withReauth(() => downloadFile(url, filename, init))
}
