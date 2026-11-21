// 新建任务时沿用上一次在 Web UI 中保存的配置。
// 只保存非一次性、非敏感的配置；域名、任务名、DNS 路由快照和凭据字段不进入快照。
import { deployTypeDef, normalizeDeployConfig } from './deployConfig'
import type { NotifyChannelMeta } from './notifyChannels'
import { readStorage, removeStorage, writeStorage } from './storage'
import type {
  DeployAccount,
  DNSAccount,
  FlowMode,
  Meta,
  NotifyOn,
  PipelineNode,
  Settings
} from './types'

const STORAGE_KEY = 'lactssl.tasks.lastConfig.v1'
const MAX_PIPELINE_NODES = 50
const MAX_NODE_NAME = 40
const MAX_SUBJECT = 80
const MIN_RENEW_DAYS = 1
const MAX_RENEW_DAYS = 60

export interface ReuseContext {
  dnsAccounts: DNSAccount[]
  deployAccounts: DeployAccount[]
  meta: Meta | null
  settings: Settings | null
  notifyChannels: NotifyChannelMeta[]
}

// 与 Tasks.vue 的 TaskForm 保持结构兼容，但不包含 name/domainsText/dns_routes/enabled。
export interface CreateDefaults {
  flow_mode: FlowMode
  ca_mode: 'single' | 'pool'
  ca: string
  ca_pool_id: number | null
  key_type: string
  no_key_storage: boolean
  auto_renew: boolean
  renew_days: number
  dns_account_id: number
  pipeline: PipelineNode[]
}

export interface LastTaskConfig {
  version: 1
  saved_at: string
  flow_mode: FlowMode
  ca_mode: 'single' | 'pool'
  ca: string
  ca_pool_id: number | null
  key_type: string
  no_key_storage: boolean
  auto_renew: boolean
  renew_days: number
  dns_account_id: number
  pipeline: PipelineNode[]
}

export type LastTaskConfigSource = Omit<
  LastTaskConfig,
  'version' | 'saved_at' | 'dns_account_id'
> & { dns_account_id: number | null }

let idSeq = 0

function uid(prefix: string): string {
  idSeq += 1
  return `${prefix}-${Date.now()}-${idSeq}`
}

function clampRenewDays(value: unknown, fallback: number): number {
  const numeric = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(numeric)) return fallback
  return Math.min(MAX_RENEW_DAYS, Math.max(MIN_RENEW_DAYS, Math.trunc(numeric)))
}

function cleanName(value: unknown): string {
  if (typeof value !== 'string') return ''
  return Array.from(value.trim()).slice(0, MAX_NODE_NAME).join('')
}

function cleanSubject(value: unknown): string {
  if (typeof value !== 'string') return ''
  return Array.from(value.trim()).slice(0, MAX_SUBJECT).join('')
}

function clonePipeline(nodes: PipelineNode[]): PipelineNode[] {
  return nodes.map((node) => ({ ...node, config: { ...node.config } }))
}

function applyNode(): PipelineNode {
  return { id: 'apply', type: 'apply', name: '', enabled: true, config: {} }
}

function isReusableNode(value: unknown): value is PipelineNode {
  if (!value || typeof value !== 'object') return false
  const node = value as Partial<PipelineNode>
  return (
    (node.type === 'deploy' || node.type === 'notify') &&
    !!node.config &&
    typeof node.config === 'object' &&
    !Array.isArray(node.config)
  )
}

function caIsAvailable(value: string, ctx: ReuseContext): boolean {
  if (!value || !ctx.meta) return false
  return (
    (ctx.meta.cas || []).some((item) => item.value === value) ||
    (ctx.meta.private_cas || []).some((item) => item.value === value)
  )
}

function resolveCA(
  base: CreateDefaults,
  last: LastTaskConfig,
  ctx: ReuseContext
): Pick<CreateDefaults, 'ca_mode' | 'ca' | 'ca_pool_id'> {
  if (last.ca_mode === 'pool' && last.ca_pool_id) {
    const pool = (ctx.meta?.ca_pools || []).find((item) => item.id === last.ca_pool_id)
    if (pool?.enabled && pool.count > 0) {
      return { ca_mode: 'pool', ca: '', ca_pool_id: pool.id }
    }
  }
  if (last.ca_mode === 'single' && caIsAvailable(last.ca, ctx)) {
    return { ca_mode: 'single', ca: last.ca, ca_pool_id: null }
  }
  return { ca_mode: base.ca_mode, ca: base.ca, ca_pool_id: base.ca_pool_id }
}

function resolveKeyType(last: string, fallback: string, ctx: ReuseContext): string {
  const options = ctx.meta?.key_types || []
  if (last && options.some((item) => item.value === last)) return last
  return fallback
}

function resolveDNSAccount(last: number, ctx: ReuseContext): number {
  if (!last) return 0
  return ctx.dnsAccounts.some((item) => item.id === last) ? last : 0
}

function sanitizeDeployNode(node: PipelineNode, ctx: ReuseContext): PipelineNode | null {
  const accountID = Number(node.config?.deploy_account_id)
  if (!accountID) return null
  const account = ctx.deployAccounts.find((item) => item.id === accountID)
  if (!account) return null
  const def = deployTypeDef(account.type)
  // 插件未安装或类型已下线时，保留节点只会在保存 / 执行阶段报错，直接丢弃。
  if (!def) return null

  const normalized = normalizeDeployConfig(account.type, node.config?.deploy_config || {})
  const secretKeys = new Set(
    (def.deploy_fields || []).filter((field) => field.secret).map((field) => field.key)
  )
  const config: Record<string, string> = {}
  for (const [key, value] of Object.entries(normalized)) {
    if (!secretKeys.has(key)) config[key] = value
  }

  return {
    id: uid('deploy'),
    type: 'deploy',
    name: cleanName(node.name),
    enabled: node.enabled !== false,
    config: {
      deploy_account_id: account.id,
      deploy_config: config
    }
  }
}

function sanitizeNotifyNode(node: PipelineNode, ctx: ReuseContext): PipelineNode {
  const rawChannels = Array.isArray(node.config?.notify_channels)
    ? node.config.notify_channels
    : []
  // settings 尚未加载时不做过滤，读取 / 下次保存时会再按启用渠道收敛。
  const filterChannels = !!ctx.settings
  const enabled = new Set(ctx.notifyChannels.map((item) => item.value))
  const channels: string[] = []
  for (const raw of rawChannels) {
    if (typeof raw !== 'string') continue
    const name = raw.trim()
    if (!name || channels.includes(name)) continue
    if (filterChannels && !enabled.has(name)) continue
    channels.push(name)
  }

  const on = node.config?.notify_on
  const notifyOn: NotifyOn = on === 'success' || on === 'failure' ? on : 'always'

  return {
    id: uid('notify'),
    type: 'notify',
    name: cleanName(node.name),
    enabled: node.enabled !== false,
    config: {
      notify_channel: 'all',
      notify_channels: channels,
      notify_on: notifyOn,
      subject: cleanSubject(node.config?.subject)
    }
  }
}

function sanitizePipeline(nodes: PipelineNode[], ctx: ReuseContext): PipelineNode[] {
  const result: PipelineNode[] = []
  for (const node of nodes.slice(0, MAX_PIPELINE_NODES)) {
    if (!node || (node.type !== 'deploy' && node.type !== 'notify')) continue
    const sanitized = node.type === 'deploy' ? sanitizeDeployNode(node, ctx) : sanitizeNotifyNode(node, ctx)
    if (sanitized) result.push(sanitized)
  }
  return result
}

export function readLastTaskConfig(): LastTaskConfig | null {
  const raw = readStorage(STORAGE_KEY)
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    if (!parsed || typeof parsed !== 'object' || parsed.version !== 1) {
      removeStorage(STORAGE_KEY)
      return null
    }
    return {
      version: 1,
      saved_at: typeof parsed.saved_at === 'string' ? parsed.saved_at : '',
      flow_mode: parsed.flow_mode === 'target_beta' ? 'target_beta' : 'standard',
      ca_mode: parsed.ca_mode === 'pool' ? 'pool' : 'single',
      ca: typeof parsed.ca === 'string' ? parsed.ca : '',
      ca_pool_id:
        typeof parsed.ca_pool_id === 'number' && parsed.ca_pool_id > 0
          ? parsed.ca_pool_id
          : null,
      key_type: typeof parsed.key_type === 'string' ? parsed.key_type : '',
      no_key_storage: parsed.no_key_storage === true,
      auto_renew: typeof parsed.auto_renew === 'boolean' ? parsed.auto_renew : true,
      renew_days: clampRenewDays(parsed.renew_days, 30),
      dns_account_id:
        typeof parsed.dns_account_id === 'number' && parsed.dns_account_id > 0
          ? parsed.dns_account_id
          : 0,
      pipeline: Array.isArray(parsed.pipeline) ? parsed.pipeline.filter(isReusableNode) : []
    }
  } catch (_) {
    // 隐私模式、配额或历史脏数据都不应阻止新建任务。
    removeStorage(STORAGE_KEY)
    return null
  }
}

export function writeLastTaskConfig(source: LastTaskConfigSource, ctx: ReuseContext): void {
  // 写入时先清洗一次，避免把失效引用和 secret 字段落到 localStorage。
  // Beta 流程不执行部署节点，快照也不保留它们，避免切回标准流程时出现幽灵模块。
  const nodes =
    source.flow_mode === 'target_beta'
      ? source.pipeline.filter((node) => node.type !== 'deploy')
      : source.pipeline
  const config: LastTaskConfig = {
    version: 1,
    saved_at: new Date().toISOString(),
    flow_mode: source.flow_mode === 'target_beta' ? 'target_beta' : 'standard',
    ca_mode: source.ca_mode === 'pool' ? 'pool' : 'single',
    ca: source.ca_mode === 'pool' ? '' : String(source.ca || ''),
    ca_pool_id:
      source.ca_mode === 'pool' && source.ca_pool_id ? source.ca_pool_id : null,
    key_type: String(source.key_type || 'ecdsa'),
    no_key_storage: !!source.no_key_storage,
    auto_renew: !!source.auto_renew,
    renew_days: clampRenewDays(source.renew_days, 30),
    dns_account_id:
      typeof source.dns_account_id === 'number' && source.dns_account_id > 0
        ? source.dns_account_id
        : 0,
    pipeline: sanitizePipeline(nodes || [], ctx)
  }
  writeStorage(STORAGE_KEY, JSON.stringify(config))
}

export function buildCreateDefaults(
  base: CreateDefaults,
  last: LastTaskConfig | null,
  ctx: ReuseContext
): CreateDefaults {
  if (!last) {
    return { ...base, pipeline: clonePipeline(base.pipeline) }
  }

  const ca = resolveCA(base, last, ctx)
  const candidates =
    last.flow_mode === 'target_beta'
      ? last.pipeline.filter((node) => node.type !== 'deploy')
      : last.pipeline
  const sanitized = sanitizePipeline(candidates, ctx)
  // 上次确实有模块但全部失效时回退默认流程；上次本来就没有模块时尊重“只有申请节点”。
  const pipeline =
    candidates.length > 0 && sanitized.length === 0
      ? clonePipeline(base.pipeline)
      : [applyNode(), ...sanitized]

  return {
    flow_mode: last.flow_mode === 'target_beta' ? 'target_beta' : 'standard',
    ca_mode: ca.ca_mode,
    ca: ca.ca,
    ca_pool_id: ca.ca_pool_id,
    key_type: resolveKeyType(last.key_type, base.key_type, ctx),
    no_key_storage:
      typeof last.no_key_storage === 'boolean' ? last.no_key_storage : base.no_key_storage,
    auto_renew: typeof last.auto_renew === 'boolean' ? last.auto_renew : base.auto_renew,
    renew_days: clampRenewDays(last.renew_days, base.renew_days),
    dns_account_id: resolveDNSAccount(last.dns_account_id, ctx),
    pipeline
  }
}
