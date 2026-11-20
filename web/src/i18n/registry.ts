import type { Language } from 'element-plus/es/locale'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'
import ru from 'element-plus/es/locale/lang/ru'
import ja from 'element-plus/es/locale/lang/ja'

export interface LocaleMeta {
  label: string
  dir: 'ltr' | 'rtl'
  element: Language
}

// 新增语言时，在 i18n/locales 下新增翻译文件，并在这里补充语言名称与 Element Plus 映射。
export const localeMeta: Record<string, LocaleMeta> = {
  'zh-CN': { label: '简体中文', dir: 'ltr', element: zhCn },
  'en-US': { label: 'English', dir: 'ltr', element: en },
  'ru-RU': { label: 'Русский', dir: 'ltr', element: ru },
  'ja-JP': { label: '日本語', dir: 'ltr', element: ja }
}

export const fallbackLocale = 'zh-CN'

// 俄语、日语等非中英语言默认不参与一致性校验；缺少文案时先回退英文，再回退默认语言。
// vue-i18n 通过 default 键为未单独列出的语言提供回退。
export const localeFallbacks: Record<string, string[]> = {
  default: [fallbackLocale],
  'ru-RU': ['en-US', fallbackLocale],
  'ja-JP': ['en-US', fallbackLocale]
}
