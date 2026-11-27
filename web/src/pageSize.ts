import { ref, watch, type Ref } from 'vue'
import { readStorage, writeStorage } from './storage'

// 内置的每页条数与日志/归档接口的上限保持一致（后端对 size>200 会回退默认值）。
export const PAGE_SIZE_OPTIONS = [25, 50, 100, 200]

const STORAGE_PREFIX = 'lactssl.pageSize.'

function normalize(value: number, min: number, max: number, fallback: number): number {
  if (!Number.isFinite(value)) return fallback
  const rounded = Math.floor(value)
  if (rounded < min) return fallback
  if (rounded > max) return max
  return rounded
}

/**
 * 每页条数偏好：首次使用默认值，之后写入 localStorage，
 * 刷新页面或重新进入列表时沿用上次选择。
 */
export function usePageSize(key: string, fallback: number, min = 1, max = 200): Ref<number> {
  const storageKey = STORAGE_PREFIX + key
  const raw = readStorage(storageKey)
  const stored = raw === null || raw.trim() === '' ? Number.NaN : Number(raw)
  const value = ref(normalize(stored, min, max, fallback))
  watch(value, (next) => {
    writeStorage(storageKey, String(next))
  })
  return value
}
