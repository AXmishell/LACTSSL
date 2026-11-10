// 前后端共享的数据结构定义，字段与 Go 侧的 JSON 输出保持一致。

export interface User {
  id: number
  email: string
  totp_enabled: boolean
  created_at: string
  updated_at: string
}

export interface Session {
  id: string
  ip: string
  user_agent: string
  created_at: string
  last_seen_at: string
  expires_at: string
  current: boolean
}

export interface FieldOption {
  label: string
  value: string
}

export interface FieldCondition {
  key: string
  value: string
}

export interface FieldDef {
  key: string
  label: string
  type: 'text' | 'password' | 'textarea' | 'number' | 'select' | 'switch'
  required?: boolean
  placeholder?: string
  help?: string
  options?: FieldOption[]
  default?: string
  show_if?: FieldCondition
}

export interface ProviderDef {
  value: string
  label: string
  desc?: string
  fields: FieldDef[]
  deploy_fields?: FieldDef[]
}

export interface CADef {
  value: string
  label: string
  url?: string
  eab?: boolean
  desc?: string
}

export interface CAConfig extends CADef {
  eab_kid: string
  eab_hmac_set: boolean
  is_default: boolean
}

export interface PrivateCA {
  id: number
  name: string
  common_name: string
  organization: string
  country: string
  key_type: string
  source: 'generated' | 'imported' | string
  serial: string
  parent_id: number
  parent_name: string
  ca_type: 'root' | 'intermediate' | string
  path_len: number
  cert_pem?: string
  not_before: string
  not_after: string
  created_at: string
  updated_at: string
}

export interface PrivateCAOption {
  value: string
  label: string
  ca_type?: string
  path_len?: number
  not_after?: string
}

export interface Meta {
  version?: string
  language?: string
  languages?: FieldOption[]
  cas?: CADef[]
  private_cas?: PrivateCAOption[]
  key_types?: FieldOption[]
  dns_providers?: ProviderDef[]
  deploy_types?: ProviderDef[]
}

export interface Settings {
  entry?: string
  language?: string
  acme_email?: string
  acme_ca?: string
  acme_key_type?: string
  acme_renew_days?: number
  smtp_host?: string
  smtp_port?: number
  smtp_username?: string
  smtp_from?: string
  smtp_ssl?: boolean
  smtp_password_set?: boolean
  notify_enabled?: boolean
  notify_to?: string
  gotify_url?: string
  gotify_token_set?: boolean
  gotify_enabled?: boolean
  gotify_priority?: number
  serverchan_enabled?: boolean
  serverchan_sendkey_set?: boolean
  serverchan_version?: string
  notify_channels?: Record<string, Record<string, any>>
}

export interface DNSAccount {
  id: number
  name: string
  provider: string
  config: Record<string, string>
  remark: string
  created_at: string
  updated_at: string
}

export interface DeployAccount {
  id: number
  name: string
  type: string
  config: Record<string, string>
  remark: string
  created_at: string
  updated_at: string
}

export type Account = DNSAccount | DeployAccount

export type PipelineNodeType = 'apply' | 'deploy' | 'notify'
export type NotifyOn = 'always' | 'success' | 'failure'

export interface PipelineConfig {
  domains?: string[]
  dns_account_id?: number
  ca?: string
  key_type?: string
  auto_renew?: boolean
  renew_days?: number
  deploy_account_id?: number
  deploy_config?: Record<string, string>
  notify_channel?: string
  notify_channels?: string[]
  notify_on?: NotifyOn
  subject?: string
}

export interface PipelineNode {
  id: string
  type: PipelineNodeType
  name: string
  enabled: boolean
  config: PipelineConfig
}

export interface Task {
  id: number
  name: string
  domains: string[]
  challenge: string
  dns_account_id: number
  ca: string
  key_type: string
  auto_renew: boolean
  renew_days: number
  deploy_ids: number[]
  pipeline: PipelineNode[]
  enabled: boolean
  status: string
  message: string
  last_run_at: string | null
  next_run_at: string | null
  cert_id: number
  created_at: string
  updated_at: string
}

export interface Certificate {
  id: number
  task_id: number
  domains: string[]
  primary_domain: string
  issuer: string
  ca: string
  source?: string
  cert_pem: string
  key_pem: string
  fullchain_pem: string
  serial: string
  not_before: string
  not_after: string
  status: string
  revoked_at?: string
  created_at: string
}

export interface ChainItem {
  subject: string
  issuer: string
  is_ca: boolean
  not_before: string
  not_after: string
  signature_algorithm: string
  public_key: string
  serial_number: string
}

export interface CertificateAnalysis {
  subject: string
  issuer: string
  serial: string
  signature_algorithm: string
  public_key_algorithm: string
  key_bits: number
  not_before: string
  not_after: string
  days_left: number
  dns_names: string[]
  is_wildcard: boolean
  chain: ChainItem[]
  issues: string[]
  level: string
  score: number
  validity_days: number
}

export interface DeployRecord {
  id: number
  cert_id: number
  deploy_account_id: number
  account_name: string
  task_id: number
  status: string
  message: string
  created_at: string
}

export interface Monitor {
  id: number
  name: string
  target: string
  interval_hours: number
  enabled: boolean
  status: string
  message: string
  expires_at: string | null
  issuer: string
  last_check_at: string | null
  next_check_at: string | null
  created_at: string
}

export interface LogEntry {
  id: number
  source: string
  ref_id: number
  level: string
  message: string
  created_at: string
}

export interface ExpiryBucket {
  label: string
  count: number
}

export interface TrendPoint {
  date: string
  count: number
}

export interface Dashboard {
  cert_total: number
  cert_expiring: number
  cert_expired: number
  task_total: number
  task_enabled: number
  monitor_total: number
  monitor_issue: number
  deploy_total: number
  success_rate: number
  expiry_buckets: ExpiryBucket[]
  trend: TrendPoint[]
  recent_tasks: Task[]
  recent_certs: Certificate[]
  recent_logs: LogEntry[]
}

// 会话令牌通过 HttpOnly Cookie 下发，响应体只返回用户信息。
export interface AuthResult {
  user: User
}

export interface MessageResult {
  message?: string
  entry?: string
  restarting?: boolean
}

export interface TaskBatchResult {
  started?: number
  skipped?: number
  updated?: number
  skipped_ids?: number[]
  message?: string
}

export interface BackupInfo {
  database_size: number
  config_size: number
  config_included: boolean
  pending_restore: boolean
  last_backup_at: string | null
}

export interface TOTPSetup {
  secret: string
  url: string
}

export interface LogPage {
  items: LogEntry[]
  total: number
}

export interface DeployResult {
  success: boolean
  name: string
  message: string
}

export interface DeployResponse {
  results: DeployResult[]
}

export interface SetupStatus {
  initialized: boolean
}

export interface ApiError extends Error {
  data?: unknown
  status?: number
  totpRequired?: boolean
}

export interface ACMEEndpoint {
  id: number
  public_id: string
  name: string
  slug: string
  ca_id: number
  ca_name: string
  enabled: boolean
  eab_required: boolean
  allowed_domains: string[]
  allow_wildcard: boolean
  allow_ip: boolean
  allowed_ip_cidrs: string[]
  http01_enabled: boolean
  dns01_enabled: boolean
  tls_alpn_01_enabled: boolean
  allow_private_http01: boolean
  allow_private_tls_alpn_01: boolean
  http01_allowed_cidrs: string[]
  tls_alpn_01_allowed_cidrs: string[]
  validity_days: number
  max_orders_per_hour: number
  directory_url: string
  created_at: string
  updated_at: string
}

export interface ACMEEABCredential {
  id: number
  public_id: string
  endpoint_id: number
  kid: string
  label: string
  hmac_set: boolean
  used_at?: string | null
  expires_at?: string | null
  created_at: string
}

export interface ACMEEABCreated {
  id: number
  kid: string
  label: string
  hmac_key: string
  hmac_set: boolean
  expires_at?: string | null
  created_at: string
}

export interface ACMEAccount {
  id: number
  public_id: string
  endpoint_id: number
  endpoint_name: string
  kid: string
  thumbprint: string
  contact: string[]
  status: string
  eab_id: number
  last_used_at?: string | null
  created_at: string
  updated_at: string
}

export interface ACMEOrder {
  id: number
  public_id: string
  endpoint_id: number
  endpoint_name: string
  account_id: number
  account_kid: string
  status: string
  identifiers: string[]
  replaces?: string
  expires_at?: string | null
  certificate_id: number
  certificate_state?: string
  error: string
  created_at: string
  updated_at: string
}

export interface ACMEChallenge {
  id: number
  public_id: string
  authz_id: number
  type: string
  token: string
  status: string
  validated_at?: string | null
  error: string
  attempts: number
  next_check_at?: string | null
  created_at: string
  updated_at: string
}

export interface ACMEAuthorization {
  id: number
  public_id: string
  order_id: number
  identifier: string
  identifier_type: string
  wildcard: boolean
  status: string
  expires_at?: string | null
  created_at: string
  updated_at: string
  challenges: ACMEChallenge[]
}

export interface ACMEOrderDetail {
  order: ACMEOrder
  authorizations: ACMEAuthorization[]
  certificate?: Certificate | null
}
