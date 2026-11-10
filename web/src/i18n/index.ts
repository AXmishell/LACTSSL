import { createI18n } from 'vue-i18n'
import { localeMeta, fallbackLocale } from './registry'

export { localeMeta }

const STORAGE_KEY = 'lactssl.locale'

// 自动发现 locales 目录下的翻译文件：新增一门语言只需新增一个文件。
const modules = import.meta.glob('./locales/*.ts', { eager: true })
const messages: Record<string, any> = {}
for (const [path, module] of Object.entries(modules)) {
  const match = path.match(/([^/]+)\.ts$/)
  if (match) messages[match[1]] = (module as { default: Record<string, unknown> }).default
}

function detectLocale(): string {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored && messages[stored]) return stored
  const candidates = [navigator.language, ...(navigator.languages || [])]
  for (const candidate of candidates) {
    if (!candidate) continue
    if (messages[candidate]) return candidate
    const base = candidate.toLowerCase().split('-')[0]
    const match = Object.keys(messages).find((locale) => locale.toLowerCase().split('-')[0] === base)
    if (match) return match
  }
  return fallbackLocale
}

export const i18n = createI18n({
  legacy: false,
  locale: detectLocale(),
  fallbackLocale,
  messages,
  missingWarn: false,
  fallbackWarn: false
} as any)

function localeRef(): any {
  return (i18n.global as any).locale
}

export function currentLocale(): string {
  const ref = localeRef()
  return String(ref && typeof ref === 'object' && 'value' in ref ? ref.value : ref)
}

export function availableLocales(): string[] {
  return Object.keys(messages).filter((locale) => !!localeMeta[locale])
}

export function elementLocaleFor(locale: string) {
  return localeMeta[locale]?.element || localeMeta[fallbackLocale].element
}

export function setLocale(locale: string): void {
  const target = messages[locale] ? locale : fallbackLocale
  const ref = localeRef()
  if (ref && typeof ref === 'object' && 'value' in ref) ref.value = target
  else (i18n.global as any).locale = target
  localStorage.setItem(STORAGE_KEY, target)
  const meta = localeMeta[target]
  document.documentElement.lang = target
  document.documentElement.dir = meta?.dir || 'ltr'
}

// 页面启动时同步一次 <html lang/dir>，确保无障碍与字体选择正确。
setLocale(currentLocale())

export function t(key: string, params?: Record<string, unknown>): string {
  const global = i18n.global as any
  return global.t(key, params || {})
}
