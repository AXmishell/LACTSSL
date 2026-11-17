// 安全的 localStorage 包装：隐私模式、企业策略或配额异常时读写会抛错，
// 这里退化为无存储，避免模块初始化失败导致整页停在加载层。
export function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch (_) {
    return null
  }
}

export function writeStorage(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value)
  } catch (_) {
    // 忽略：语言/偏好无法持久化不应影响页面运行。
  }
}

export function removeStorage(key: string): void {
  try {
    window.localStorage.removeItem(key)
  } catch (_) {
    // 忽略。
  }
}
