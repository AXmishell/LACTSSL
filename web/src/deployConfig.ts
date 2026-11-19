import { state } from './store'
import type { DeployAccount, FieldDef, ProviderDef } from './types'
import { fieldVisible } from './utils'

// 部署目标的元数据来自全局 meta，登录后由 App 加载。
export function deployTypeDef(type?: string): ProviderDef | undefined {
  return (state.meta?.deploy_types || []).find((item) => item.value === type)
}

export function deployLabel(value?: string): string {
  return deployTypeDef(value)?.label || value || ''
}

export function defaultDeployConfig(type?: string): Record<string, string> {
  const config: Record<string, string> = {}
  for (const field of deployTypeDef(type)?.deploy_fields || []) {
    if (field.default) config[field.key] = field.default
    else if (field.type === 'select' && field.options?.length) config[field.key] = field.options[0].value
    else config[field.key] = ''
  }
  return config
}

export function normalizeDeployConfig(
  type: string | undefined,
  current: Record<string, string> | undefined
): Record<string, string> {
  const def = deployTypeDef(type)
  // 插件未安装时元数据不在列表中，保留原参数，避免编辑任务时误清空。
  if (!def) return { ...(current || {}) }
  const fields = def.deploy_fields || []
  const allowed = new Set(fields.map((field) => field.key))
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(current || {})) {
    if (allowed.has(key)) out[key] = value
  }
  for (const [key, value] of Object.entries(defaultDeployConfig(type))) {
    if (out[key] === undefined) out[key] = value
  }
  // 合并目标必须显式带上部署对象，否则站点字段可能不可见。
  if (fields.some((field) => field.key === 'target') && !out.target) {
    out.target = defaultDeployMode(type)
  }
  return out
}

function defaultDeployMode(type?: string): string {
  switch (type) {
    case 'baota':
    case '1panel':
    case 'acepanel':
      return 'site'
    case 'qiniu':
      return 'cdn'
    case 'tencent':
    case 'aliyun':
    case 'volcengine':
      return 'cdn'
    default:
      return ''
  }
}

// deployObjectFieldKey 返回写入站点名称的部署字段。
// 不同面板使用 sites / website / websites，最终以插件元数据为准。
export function deployObjectFieldKey(type: string | undefined, config: Record<string, string> | undefined): string {
  const def = deployTypeDef(type)
  if (!def) return ''
  const fields = def.deploy_fields || []
  const preferred = ['sites', 'website', 'websites']
  for (const key of preferred) {
    const field = fields.find((item) => item.key === key)
    if (field && fieldVisible(field, fields, config)) return field.key
  }
  const fallback = fields.find(
    (field) => fieldVisible(field, fields, config) && /(^|_)(site|sites|website|websites|site_name|website_name|site_id|website_id)$/i.test(field.key)
  )
  return fallback?.key || ''
}

export function siteFieldOf(account: DeployAccount | undefined, config: Record<string, string> | undefined): string {
  if (!account) return ''
  return deployObjectFieldKey(account.type, config)
}

export function isSiteDeployAccount(account: DeployAccount): boolean {
  return !!siteFieldOf(account, normalizeDeployConfig(account.type, account.config))
}

export function visibleDeployFields(type: string | undefined, config: Record<string, string> | undefined): FieldDef[] {
  const fields = deployTypeDef(type)?.deploy_fields || []
  return fields.filter((field) => fieldVisible(field, fields, config))
}
