import { reactive } from 'vue'
import { api } from './api'
import type { Meta, Settings, User } from './types'

export interface AppState {
  user: User | null
  meta: Meta | null
  settings: Settings | null
}

// 会话令牌存放在 HttpOnly Cookie 中，前端只保留内存状态。
export const state = reactive<AppState>({
  user: null,
  meta: null,
  settings: null
})

export function logout(): void {
  state.user = null
  state.settings = null
  state.meta = null
}

export async function loadUser(force = false): Promise<User | null> {
  if (state.user && !force) return state.user
  state.user = await api.get<User>('/auth/me')
  return state.user
}

export async function loadMeta(force = false): Promise<Meta> {
  if (state.meta && !force) return state.meta
  state.meta = await api.get<Meta>('/meta')
  return state.meta
}

export async function loadSettings(force = false): Promise<Settings> {
  if (state.settings && !force) return state.settings
  state.settings = await api.get<Settings>('/settings')
  return state.settings
}

// 令牌失效时同步清理内存状态，避免页面残留登录信息。
window.addEventListener('lactssl:unauthorized', () => {
  state.user = null
  state.settings = null
  state.meta = null
})
