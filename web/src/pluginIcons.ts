import { reactive } from 'vue'
import { api } from './api'

// 服务商图标由插件仓库下发（索引内联 + 已安装包内文件），主程序不再内置资源表。
// 加载失败时保持空表，页面回退到首字母头像，不阻塞凭据与插件管理功能。
type IconMap = Record<string, Record<string, string>>

const state = reactive<{ icons: IconMap; loaded: boolean }>({
  icons: {},
  loaded: false
})

let pending: Promise<void> | null = null

export async function loadPluginIcons(force = false): Promise<void> {
  if (state.loaded && !force) return
  if (pending) return pending
  pending = (async () => {
    try {
      const result = await api.get<IconMap>('/plugins/icons')
      state.icons = result || {}
      state.loaded = true
    } catch {
      state.icons = {}
    } finally {
      pending = null
    }
  })()
  return pending
}

export function pluginIcon(kind: string | undefined, id?: string): string {
  if (!kind || !id) return ''
  return state.icons[kind]?.[id] || ''
}
