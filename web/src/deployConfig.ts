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

// deployDomainMatchesSite 判断网站名称是否属于证书覆盖的域名。
// 与后端 siteMatchesDomains 保持一致：通配符证书覆盖同一主域名下的子域名。
export function deployDomainMatchesSite(site: string, domains: string[]): boolean {
  const name = normalizeDeployDomain(site)
  if (!name) return false
  for (const item of domains) {
    const domain = normalizeDeployDomain(item)
    if (!domain) continue
    if (name === domain) return true
    if (domain.startsWith('*.')) {
      const base = domain.slice(2)
      if (name === base || name.endsWith(`.${base}`)) return true
    }
  }
  return false
}

function normalizeDeployDomain(value: string): string {
  return String(value || '').trim().toLowerCase().replace(/\.$/, '')
}

export function siteFieldOf(account: DeployAccount | undefined, config: Record<string, string> | undefined): string {
  if (!account) return ''
  return deployObjectFieldKey(account.type, config)
}

export function isSiteDeployAccount(account: DeployAccount): boolean {
  return !!siteFieldOf(account, normalizeDeployConfig(account.type, account.config))
}

// 目标流程 Beta 的站点来源必须是部署插件声明了 plan-v1 能力、
// 且当前配置能读到站点字段的目标。能力位完全来自插件 manifest，不在前端维护白名单。
export const PLAN_CAPABILITY = 'plan-v1'

export function supportsTargetFlowPlan(type: string | undefined): boolean {
  const def = deployTypeDef(type)
  return !!def?.capabilities?.includes(PLAN_CAPABILITY)
}

export function isTargetFlowSource(account: DeployAccount): boolean {
  return supportsTargetFlowPlan(account.type) && isSiteDeployAccount(account)
}

export function visibleDeployFields(type: string | undefined, config: Record<string, string> | undefined): FieldDef[] {
  const fields = deployTypeDef(type)?.deploy_fields || []
  return fields.filter((field) => fieldVisible(field, fields, config))
}
