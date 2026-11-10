import type { Language } from 'element-plus/es/locale'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'

export interface LocaleMeta {
  label: string
  dir: 'ltr' | 'rtl'
  element: Language
}

// 新增语言时，在 i18n/locales 下新增翻译文件，并在这里补充语言名称与 Element Plus 映射。
export const localeMeta: Record<string, LocaleMeta> = {
  'zh-CN': { label: '简体中文', dir: 'ltr', element: zhCn },
  'en-US': { label: 'English', dir: 'ltr', element: en }
}

export const fallbackLocale = 'zh-CN'
