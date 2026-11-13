<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('certs.title') }}</h1>
        <p>{{ t('certs.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-input v-model="keyword" :placeholder="t('certs.searchDomain')" clearable style="width: 200px">
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-button type="warning" plain :icon="Delete" @click="openCleanup">{{ t('certs.cleanup') }}</el-button>
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <div class="cert-list">
      <div class="k-card" style="padding: 0; overflow: hidden">
        <div class="filter-row">
          <el-radio-group v-model="filter" size="default">
            <el-radio-button value="all">{{ t('certs.all', { count: certs.length }) }}</el-radio-button>
            <el-radio-button value="valid">{{ t('certs.valid', { count: counts.valid }) }}</el-radio-button>
            <el-radio-button value="expiring">{{ t('certs.expiring30', { count: counts.expiring }) }}</el-radio-button>
            <el-radio-button value="expired">{{ t('certs.expired', { count: counts.expired }) }}</el-radio-button>
          </el-radio-group>
          <span class="muted">{{ t('certs.count', { count: filtered.length }) }}</span>
        </div>

        <el-table
          ref="tableRef"
          :data="filtered"
          row-key="id"
          v-loading="loading"
          style="width: 100%"
          @selection-change="onSelectionChange"
        >
          <el-table-column type="selection" width="48" fixed="left" :reserve-selection="true" />
          <el-table-column min-width="230" :label="t('certs.primaryDomain')">
            <template #default="{ row }">
              <div class="domain-cell">
                <span class="domain-main">{{ row.primary_domain }}</span>
                <span class="domain-sub">{{ t('certs.domainsCount', { count: row.domains?.length || 1, issuer: row.issuer || t('certs.unknownIssuer') }) }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column width="140" :label="t('certs.issuer')">
            <template #default="{ row }">
              <span class="tag-soft tag-gray">{{ caLabel(row.ca) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="190" :label="t('certs.expiresAt')">
            <template #default="{ row }">
              <div class="domain-cell">
                <span>{{ formatDate(row.not_after) }}</span>
                <span class="days-left">{{ t('certs.remainingDays', { days: daysLeft(row.not_after) }) }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column width="120" :label="t('certs.status')">
            <template #default="{ row }">
              <el-tag :type="certState(row).type" effect="light" round size="small">{{ certState(row).label }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column width="150" :label="t('certs.issuedAt')">
            <template #default="{ row }">
              <span class="muted">{{ formatDate(row.created_at) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="255" :label="t('certs.actions')" fixed="right">
            <template #default="{ row }">
              <el-button text type="primary" size="small" @click="openDetail(row)">{{ t('certs.details') }}</el-button>
              <el-dropdown trigger="click" @command="(kind: string) => download(row, kind)">
                <el-button text size="small">{{ t('certs.download') }}<el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="fullchain">{{ t('certs.fullchain') }}</el-dropdown-item>
                    <el-dropdown-item command="cert">{{ t('certs.cert') }}</el-dropdown-item>
                    <el-dropdown-item v-if="row.source !== 'acme'" command="key">{{ t('certs.key') }}</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
              <el-button v-if="row.source !== 'acme'" text size="small" @click="openDeploy(row)">{{ t('certs.deploy') }}</el-button>
              <el-button text type="danger" size="small" @click="remove(row)">{{ t('common.delete') }}</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div style="padding: 40px 0; color: #94a3b8">
              <el-icon size="30"><Document /></el-icon>
              <p>{{ t('certs.empty') }}</p>
            </div>
          </template>
        </el-table>
      </div>
    </div>

    <div v-if="selectedCerts.length" class="batch-bar">
      <div class="batch-actions">
        <el-button size="small" type="primary" plain :icon="Download" :loading="batchDownloading" @click="batchDownload">
          {{ t('certs.batchDownload') }}
        </el-button>
        <el-button size="small" :icon="UploadFilled" @click="openBatchDeploy">
          {{ t('certs.batchDeploy') }}
        </el-button>
        <el-button size="small" type="danger" plain :icon="Delete" :loading="batchDeleting" @click="batchRemove">
          {{ t('certs.batchDelete') }}
        </el-button>
        <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('certs.clearSelection') }}</el-button>
      </div>
      <span class="batch-count">{{ t('certs.selectedCount', { count: selectedCerts.length }) }}</span>
    </div>

    <!-- 证书详情 -->
    <el-drawer v-model="detailVisible" size="760px" :title="detail?.primary_domain || t('certs.detailTitle')">
      <div v-loading="detailLoading">
        <div class="detail-top">
          <div>
            <div class="flex gap-10 wrap">
              <el-tag :type="certState(detail).type" effect="light" round>{{ certState(detail).label }}</el-tag>
              <span class="tag-soft tag-gray">{{ caLabel(detail?.ca) }}</span>
              <span class="tag-soft tag-blue">{{ detail?.issuer }}</span>
            </div>
            <div class="muted mt-12">
              {{ t('certs.validityRange', { from: formatDate(detail?.not_before, false), to: formatDate(detail?.not_after, false), days: daysLeft(detail?.not_after) }) }}
            </div>
          </div>
          <div class="flex gap-10">
            <el-button :icon="Download" @click="download(detail, 'fullchain')">{{ t('certs.download') }}</el-button>
            <el-button v-if="detail?.source !== 'acme'" type="primary" :icon="UploadFilled" @click="openDeploy(detail)">{{ t('certs.deploy') }}</el-button>
          </div>
        </div>

        <el-tabs v-model="detailTab" style="margin-top: 10px">
          <el-tab-pane :label="t('certs.overview')" name="overview">
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item :label="t('certs.primaryDomain')">{{ detail?.primary_domain }}</el-descriptions-item>
              <el-descriptions-item :label="t('certs.serial')"><span class="mono break-all">{{ detail?.serial }}</span></el-descriptions-item>
              <el-descriptions-item :label="t('certs.includedDomains')" :span="2">
                <div class="flex wrap gap-6">
                  <span v-for="domain in detail?.domains || []" :key="domain" class="tag-soft tag-gray">{{ domain }}</span>
                </div>
              </el-descriptions-item>
              <el-descriptions-item :label="t('certs.notBefore')">{{ formatDate(detail?.not_before) }}</el-descriptions-item>
              <el-descriptions-item :label="t('certs.notAfter')">{{ formatDate(detail?.not_after) }}</el-descriptions-item>
              <el-descriptions-item :label="t('certs.issuer')">{{ detail?.issuer }}</el-descriptions-item>
              <el-descriptions-item :label="t('certs.caId')">{{ caLabel(detail?.ca) }}</el-descriptions-item>
              <el-descriptions-item :label="t('certs.savedAt')" :span="2">{{ formatDate(detail?.created_at) }}</el-descriptions-item>
            </el-descriptions>

            <div class="analysis-box mt-20" v-if="analysis">
              <div class="score-ring" :style="{ background: scoreGradient }">
                <span :style="{ color: levelInfo(analysis.level).color }">{{ analysis.score }}</span>
              </div>
              <div style="flex: 1">
                <div class="flex gap-10">
                  <strong>{{ t('certs.securityScore') }}</strong>
                  <span class="tag-soft" :class="levelInfo(analysis.level).tag">{{ levelInfo(analysis.level).label }}</span>
                </div>
                <div class="muted mt-12">
                  {{ t('certs.keyInfo', { algorithm: analysis.public_key_algorithm, bits: analysis.key_bits }) }} ·
                  {{ t('certs.signatureInfo', { algorithm: analysis.signature_algorithm }) }} ·
                  {{ t('certs.validityInfo', { days: analysis.validity_days }) }}
                </div>
                <div v-if="analysis.issues?.length" class="issue-list">
                  <div v-for="issue in analysis.issues" :key="issue" class="issue-item">
                    <el-icon color="#d97706"><Warning /></el-icon>{{ issue }}
                  </div>
                </div>
                <div v-else class="issue-item ok">
                  <el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('certs.noIssues') }}
                </div>
              </div>
            </div>
          </el-tab-pane>

          <el-tab-pane :label="t('certs.chainTab')" name="chain">
            <div v-if="!analysis?.chain?.length" class="muted">{{ t('certs.noAnalysis') }}</div>
            <div v-for="(item, index) in analysis?.chain || []" :key="index" class="chain-item">
              <div class="flex-between">
                <strong>#{{ index + 1 }} {{ index === 0 ? t('certs.leaf') : item.is_ca ? t('certs.intermediate') : t('certs.chain') }}</strong>
                <span class="tag-soft" :class="item.is_ca ? 'tag-blue' : 'tag-green'">{{ item.is_ca ? 'CA' : 'Leaf' }}</span>
              </div>
              <div class="muted mt-12 break-all">{{ t('certs.subject', { subject: item.subject }) }}</div>
              <div class="muted break-all">{{ t('certs.chainIssuer', { issuer: item.issuer }) }}</div>
              <div class="muted">{{ t('certs.validity', { from: formatDate(item.not_before, false), to: formatDate(item.not_after, false) }) }}</div>
              <div class="muted">{{ t('certs.algorithm', { signature: item.signature_algorithm, key: item.public_key }) }}</div>
            </div>
          </el-tab-pane>

          <el-tab-pane :label="t('certs.pemTab')" name="pem">
            <el-radio-group v-model="pemTab" size="small" style="margin-bottom: 12px">
              <el-radio-button value="fullchain">{{ t('certs.fullchain') }}</el-radio-button>
              <el-radio-button v-if="detail?.source !== 'acme'" value="key">{{ t('certs.key') }}</el-radio-button>
            </el-radio-group>
            <div class="flex gap-10" style="margin-bottom: 10px">
              <el-button size="small" :icon="CopyDocument" @click="copyPem">{{ t('certs.copyContent') }}</el-button>
              <el-button size="small" :icon="Download" @click="download(detail, pemTab)">{{ t('certs.downloadFile') }}</el-button>
              <span class="muted" style="font-size: 12px">{{ t('certs.keyWarning') }}</span>
            </div>
            <div class="pem-box">{{ pemContent }}</div>
          </el-tab-pane>
        </el-tabs>
      </div>
    </el-drawer>

    <!-- 部署 -->
    <el-dialog
      v-model="deployVisible"
      :title="deployCerts.length > 1 ? t('certs.batchDeployTitle', { count: deployCerts.length }) : t('certs.deployTitle')"
      width="720px"
    >
      <p class="muted" style="margin-top: 0">
        {{ deployCerts.length > 1 ? t('certs.batchDeployDesc', { count: deployCerts.length }) : t('certs.deployDesc') }}
      </p>
      <div v-for="(target, index) in deployTargets" :key="index" class="deploy-target">
        <div class="deploy-target-head">
          <el-select
            v-model="target.deploy_account_id"
            :placeholder="t('certs.deployPlaceholder')"
            style="width: 100%"
            @change="onDeployTargetAccountChange(target)"
          >
            <el-option v-for="item in deployAccounts" :key="item.id" :label="`${item.name} · ${deployLabel(item.type)}`" :value="item.id" />
          </el-select>
          <el-button text type="danger" :icon="Delete" :disabled="deployTargets.length <= 1" @click="removeDeployTarget(index)" />
        </div>
        <el-form label-position="top">
          <DeployConfigFields
            v-if="deployAccountFor(target)"
            :account-type="deployAccountFor(target)?.type || ''"
            :model-value="target.deploy_config"
            @update:model-value="target.deploy_config = $event"
          />
        </el-form>
      </div>
      <el-button text type="primary" :icon="Plus" @click="addDeployTarget">{{ t('certs.addDeployTarget') }}</el-button>
      <div v-if="deployResults.length" class="deploy-results">
        <div v-for="(result, index) in deployResults" :key="index" class="deploy-result" :class="{ fail: !result.success }">
          <el-icon><component :is="result.success ? 'CircleCheck' : 'CircleClose'" /></el-icon>
          <div>
            <strong>{{ result.name }}</strong>
            <div class="muted">{{ result.message }}</div>
          </div>
        </div>
      </div>
      <template #footer>
        <el-button @click="deployVisible = false">{{ t('certs.close') }}</el-button>
        <el-button type="primary" :loading="deploying" @click="submitDeploy">{{ t('certs.startDeploy') }}</el-button>
      </template>
    </el-dialog>

    <!-- 一键清理 -->
    <el-dialog v-model="cleanupVisible" :title="t('certs.cleanupTitle')" width="680px">
      <el-alert :title="t('certs.cleanupIntro')" type="warning" :closable="false" show-icon />
      <el-form label-position="top" class="cleanup-form">
        <el-form-item :label="t('certs.cleanupScope')">
          <el-radio-group v-model="cleanupForm.scope">
            <el-radio-button value="expired">{{ t('certs.cleanupScopeExpired') }}</el-radio-button>
            <el-radio-button value="revoked">{{ t('certs.cleanupScopeRevoked') }}</el-radio-button>
            <el-radio-button value="all">{{ t('certs.cleanupScopeAll') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <div class="cleanup-grid">
          <el-form-item v-if="cleanupForm.scope === 'expired'" :label="t('certs.cleanupExpiredDays')">
            <el-input-number v-model="cleanupForm.expired_days" :min="0" :max="36500" controls-position="right" />
          </el-form-item>
          <el-form-item v-if="cleanupForm.scope === 'revoked'" :label="t('certs.cleanupRevokedDays')">
            <el-input-number v-model="cleanupForm.revoked_days" :min="0" :max="36500" controls-position="right" />
          </el-form-item>
          <el-form-item :label="t('certs.cleanupOlderThanDays')">
            <el-input-number v-model="cleanupForm.older_than_days" :min="0" :max="36500" controls-position="right" />
          </el-form-item>
          <el-form-item :label="t('certs.cleanupDomain')">
            <el-input v-model="cleanupForm.domain" clearable :placeholder="t('certs.cleanupDomainPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('certs.cleanupCA')">
            <el-select v-model="cleanupForm.ca" clearable :placeholder="t('certs.cleanupAny')">
              <el-option v-for="item in cleanupCAOptions" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('certs.cleanupSource')">
            <el-select v-model="cleanupForm.source" clearable :placeholder="t('certs.cleanupAny')">
              <el-option :label="t('certs.cleanupSourceTask')" value="task" />
              <el-option :label="t('certs.cleanupSourceACME')" value="acme" />
            </el-select>
          </el-form-item>
        </div>
        <div class="cleanup-note">{{ t('certs.cleanupDaysHint') }}</div>
        <el-checkbox v-model="cleanupForm.keep_latest_per_task">{{ t('certs.cleanupKeepLatest') }}</el-checkbox>
      </el-form>
      <el-alert class="cleanup-preview" :title="t('certs.cleanupPreview', { count: cleanupCount })" :type="cleanupCount ? 'error' : 'info'" :closable="false" show-icon />
      <template #footer>
        <el-button @click="cleanupVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="danger" :loading="cleaning" :disabled="cleanupCount === 0" @click="submitCleanup">{{ t('certs.cleanupStart') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Close, CopyDocument, Delete, Download, Plus, Refresh, UploadFilled } from '@element-plus/icons-vue'
import { api, copyText, downloadFile } from '../api'
import { requestSecondaryVerification, secondaryVerifyHeader, withSecondaryVerify } from '../secondaryVerify'
import { loadMeta, state } from '../store'
import DeployConfigFields from '../components/DeployConfigFields.vue'
import { certState, daysLeft, formatDate, isExpired } from '../utils'
import type { Certificate, CertificateAnalysis, DeployAccount, DeployResponse, DeployResult, Task } from '../types'

interface DeployTarget {
  deploy_account_id: number
  deploy_config: Record<string, string>
}

interface LevelInfo {
  label: string
  color: string
  tag: string
}

interface CleanupForm {
  scope: 'expired' | 'revoked' | 'all'
  expired_days: number
  revoked_days: number
  older_than_days: number
  domain: string
  ca: string
  source: string
  keep_latest_per_task: boolean
}

const DAY_MS = 86400000

const { t } = useI18n()
const loading = ref(false)
const certs = ref<Certificate[]>([])
const filter = ref('all')
const keyword = ref('')
const deployAccounts = ref<DeployAccount[]>([])

const tableRef = ref<{ clearSelection: () => void } | null>(null)
const selectedCerts = ref<Certificate[]>([])
const batchDownloading = ref(false)
const batchDeleting = ref(false)

const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<Certificate | null>(null)
const analysis = ref<CertificateAnalysis | null>(null)
const detailTab = ref('overview')
const pemTab = ref('fullchain')
const keyPem = ref('')

const deployVisible = ref(false)
const deployTargets = ref<DeployTarget[]>([])
const deploying = ref(false)
const deployResults = ref<DeployResult[]>([])
const deployCerts = ref<Certificate[]>([])
const tasks = ref<Task[]>([])

const cleanupVisible = ref(false)
const cleaning = ref(false)
const cleanupForm = reactive<CleanupForm>({
  scope: 'expired',
  expired_days: 0,
  revoked_days: 0,
  older_than_days: 0,
  domain: '',
  ca: '',
  source: '',
  keep_latest_per_task: false
})

const counts = computed(() => {
  let valid = 0, expiring = 0, expired = 0
  for (const cert of certs.value) {
    if (isExpired(cert.not_after)) expired++
    else if (daysLeft(cert.not_after) <= 30) expiring++
    else valid++
  }
  return { valid, expiring, expired }
})

const filtered = computed(() => {
  const key = keyword.value.trim().toLowerCase()
  return certs.value.filter((cert) => {
    const expired = isExpired(cert.not_after)
    const left = daysLeft(cert.not_after)
    if (filter.value === 'valid' && (expired || left <= 30)) return false
    if (filter.value === 'expiring' && (expired || left > 30)) return false
    if (filter.value === 'expired' && !expired) return false
    if (key) {
      const haystack = [cert.primary_domain, ...(cert.domains || [])].join(' ').toLowerCase()
      if (!haystack.includes(key)) return false
    }
    return true
  })
})

const cleanupCAOptions = computed(() => [
  ...(state.meta?.cas || []).map((item) => ({ value: item.value, label: item.label })),
  ...(state.meta?.private_cas || []).map((item) => ({ value: item.value, label: item.label }))
])

const cleanupCandidates = computed(() => {
  let list = certs.value.filter(cleanupMatches)
  if (cleanupForm.keep_latest_per_task) {
    const latest = new Map<number, number>()
    for (const cert of certs.value) {
      if (cert.task_id > 0 && cert.id > (latest.get(cert.task_id) || 0)) latest.set(cert.task_id, cert.id)
    }
    list = list.filter((cert) => cert.task_id <= 0 || latest.get(cert.task_id) !== cert.id)
  }
  return list
})

const cleanupCount = computed(() => cleanupCandidates.value.length)

const pemContent = computed(() => {
  if (!detail.value) return ''
  if (pemTab.value === 'key') return keyPem.value
  return detail.value.fullchain_pem
})

// 私钥不再随证书详情接口返回，切换到私钥页签时按需获取。
// 仅查看不做二次验证；下载证书文件时仍会要求验证。
async function loadKeyPem(): Promise<void> {
  const cert = detail.value
  if (!cert || keyPem.value) return
  try {
    const data = await withSecondaryVerify('certificate_key_export', (headers) =>
      api.get<{ content: string }>(`/certificates/${cert.id}/pem?type=key`, undefined, headers)
    )
    if (!data) return
    keyPem.value = data.content
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

watch([pemTab, detail], () => {
  if (pemTab.value === 'key') void loadKeyPem()
})

const scoreGradient = computed(() => {
  const score = analysis.value?.score || 0
  const color = levelInfo(analysis.value?.level).color
  return `conic-gradient(${color} 0 ${score}%, #e2e8f0 ${score}% 100%)`
})

function levelInfo(level?: string): LevelInfo {
  const map: Record<string, LevelInfo> = {
    excellent: { label: t('certs.levelExcellent'), color: '#10b981', tag: 'tag-green' },
    good: { label: t('certs.levelGood'), color: '#2563eb', tag: 'tag-blue' },
    warning: { label: t('certs.levelWarning'), color: '#f59e0b', tag: 'tag-amber' },
    danger: { label: t('certs.levelDanger'), color: '#ef4444', tag: 'tag-red' }
  }
  return (level && map[level]) || map.good
}

function caLabel(value?: string): string {
  if (!value) return t('common.unknown')
  const publicCA = (state.meta?.cas || []).find((item) => item.value === value)
  if (publicCA) return publicCA.label
  return (state.meta?.private_cas || []).find((item) => item.value === value)?.label || value
}
function deployLabel(value?: string): string {
  return (state.meta?.deploy_types || []).find((item) => item.value === value)?.label || value || ''
}
function deployTypeDef(type?: string) {
  return (state.meta?.deploy_types || []).find((item) => item.value === type)
}
function defaultDeployConfig(type?: string): Record<string, string> {
  const config: Record<string, string> = {}
  for (const field of deployTypeDef(type)?.deploy_fields || []) {
    if (field.default) config[field.key] = field.default
    else if (field.type === 'select' && field.options?.length) config[field.key] = field.options[0].value
    else config[field.key] = ''
  }
  return config
}
function normalizeDeployConfig(type: string | undefined, current: Record<string, string> | undefined): Record<string, string> {
  const fields = deployTypeDef(type)?.deploy_fields || []
  const allowed = new Set(fields.map((field) => field.key))
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(current || {})) {
    if (allowed.has(key)) out[key] = value
  }
  for (const [key, value] of Object.entries(defaultDeployConfig(type))) {
    if (out[key] === undefined) out[key] = value
  }
  return out
}
function deployAccountFor(target: DeployTarget): DeployAccount | undefined {
  return deployAccounts.value.find((item) => item.id === target.deploy_account_id)
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [list, accounts, taskList] = await Promise.all([
      api.get<Certificate[]>('/certificates'),
      api.get<DeployAccount[]>('/deploy-accounts'),
      api.get<Task[]>('/tasks')
    ])
    certs.value = list || []
    deployAccounts.value = accounts || []
    tasks.value = taskList || []
    clearSelection()
    await loadMeta(true)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

async function openDetail(row: Certificate): Promise<void> {
  detailVisible.value = true
  detailLoading.value = true
  detail.value = null
  analysis.value = null
  detailTab.value = 'overview'
  pemTab.value = 'fullchain'
  keyPem.value = ''
  try {
    const [cert, report] = await Promise.all([
      api.get<Certificate>(`/certificates/${row.id}`),
      api.get<CertificateAnalysis>(`/certificates/${row.id}/analysis`)
    ])
    detail.value = cert
    analysis.value = report
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    detailLoading.value = false
  }
}

async function download(cert: Certificate | null, kind: string): Promise<boolean> {
  if (!cert) return false
  const suffix = kind === 'key' ? 'key' : kind === 'cert' ? 'crt' : 'fullchain.crt'
  try {
    const headers: Record<string, string> = {}
    if (kind === 'key') {
      const token = await requestSecondaryVerification('certificate_key_export')
      if (token === null) return false
      if (token) headers[secondaryVerifyHeader] = token
    }
    await downloadFile(`/certificates/${cert.id}/download?type=${kind}`, `${cert.primary_domain}-${kind}.${suffix}`, { headers })
    return true
  } catch (error: any) {
    ElMessage.error(error.message)
    return false
  }
}

async function copyPem(): Promise<void> {
  const ok = await copyText(pemContent.value || '')
  if (ok) ElMessage.success(t('certs.copied'))
  else ElMessage.error(t('certs.copyFailed'))
}

function openDeploy(cert: Certificate | null): void {
  if (!cert) return
  openDeployFor([cert])
}

function openBatchDeploy(): void {
  if (!selectedCerts.value.length) return
  openDeployFor(selectedCerts.value)
}

function openDeployFor(list: Certificate[]): void {
  deployCerts.value = list
  deployResults.value = []
  const taskIds = new Set(list.map((cert) => cert.task_id))
  // 同一任务的证书复用任务里的部署模块配置；跨任务时从空白目标开始。
  const targets = taskIds.size === 1 ? deployTargetsFromTask(list[0].task_id) : []
  deployTargets.value = targets.length ? targets : [newDeployTarget()]
  deployVisible.value = true
}

function deployTargetsFromTask(taskId: number): DeployTarget[] {
  const task = tasks.value.find((item) => item.id === taskId)
  const nodes = task
    ? (task.pipeline?.length
        ? task.pipeline.filter((node) => node.type === 'deploy' && node.enabled)
        : (task.deploy_ids || []).map((id) => ({ config: { deploy_account_id: id, deploy_config: {} } })))
    : []
  return nodes
    .map((node) => {
      const account = deployAccounts.value.find((item) => item.id === node.config.deploy_account_id)
      const source = Object.keys(node.config.deploy_config || {}).length ? node.config.deploy_config : account?.config
      return {
        deploy_account_id: node.config.deploy_account_id || 0,
        deploy_config: normalizeDeployConfig(account?.type, source)
      }
    })
    .filter((target) => target.deploy_account_id > 0)
}

function newDeployTarget(): DeployTarget {
  const account = deployAccounts.value[0]
  return {
    deploy_account_id: account?.id || 0,
    deploy_config: normalizeDeployConfig(account?.type, {})
  }
}

function addDeployTarget(): void {
  deployTargets.value.push(newDeployTarget())
}

function removeDeployTarget(index: number): void {
  if (deployTargets.value.length <= 1) return
  deployTargets.value.splice(index, 1)
}

function onDeployTargetAccountChange(target: DeployTarget): void {
  const account = deployAccountFor(target)
  target.deploy_config = normalizeDeployConfig(account?.type, {})
}

async function submitDeploy(): Promise<void> {
  if (!deployTargets.value.length) {
    ElMessage.warning(t('certs.selectDeploy'))
    return
  }
  for (const target of deployTargets.value) {
    const account = deployAccountFor(target)
    if (!account) {
      ElMessage.warning(t('certs.selectDeploy'))
      return
    }
    const missing = (deployTypeDef(account.type)?.deploy_fields || []).find(
      (field) => field.required && !String(target.deploy_config?.[field.key] || '').trim()
    )
    if (missing) {
      ElMessage.warning(t('certs.deployConfigRequired', { field: missing.label }))
      return
    }
  }
  if (!deployCerts.value.length) return
  const token = await requestSecondaryVerification('deploy_execute')
  if (token === null) return
  const verifyHeaders: Record<string, string> = token ? { [secondaryVerifyHeader]: token } : {}
  deploying.value = true
  deployResults.value = []
  const results: DeployResult[] = []
  let failed = 0
  try {
    for (const cert of deployCerts.value) {
      try {
        const data = await api.post<DeployResponse>(`/certificates/${cert.id}/deploy`, { targets: deployTargets.value }, verifyHeaders)
        const mapped = (data.results || []).map((item) => ({
          ...item,
          name: deployCerts.value.length > 1 ? `${cert.primary_domain} · ${item.name}` : item.name
        }))
        results.push(...mapped)
        failed += mapped.filter((item) => !item.success).length
      } catch (error: any) {
        results.push({ success: false, name: cert.primary_domain, message: error.message })
        failed++
      }
    }
    deployResults.value = results
    if (failed) ElMessage.warning(t('certs.deployFailed', { count: failed }))
    else ElMessage.success(t('certs.deployDone'))
    load()
  } finally {
    deploying.value = false
  }
}

function cleanupMatches(cert: Certificate): boolean {
  const now = Date.now()
  if (cleanupForm.scope === 'expired') {
    const expiredAt = new Date(cert.not_after).getTime()
    if (!Number.isFinite(expiredAt) || expiredAt >= now - cleanupForm.expired_days * DAY_MS) return false
  } else if (cleanupForm.scope === 'revoked') {
    if (cert.status !== 'revoked') return false
    if (cleanupForm.revoked_days > 0) {
      const revokedAt = cert.revoked_at ? new Date(cert.revoked_at).getTime() : Number.NaN
      if (!Number.isFinite(revokedAt) || revokedAt >= now - cleanupForm.revoked_days * DAY_MS) return false
    }
  }
  if (cleanupForm.older_than_days > 0) {
    const createdAt = new Date(cert.created_at).getTime()
    if (!Number.isFinite(createdAt) || createdAt >= now - cleanupForm.older_than_days * DAY_MS) return false
  }
  const key = cleanupForm.domain.trim().toLowerCase()
  if (key) {
    const haystack = [cert.primary_domain, ...(cert.domains || [])].join(' ').toLowerCase()
    if (!haystack.includes(key)) return false
  }
  if (cleanupForm.ca && cert.ca !== cleanupForm.ca) return false
  const source = cert.source || 'task'
  if (cleanupForm.source && source !== cleanupForm.source) return false
  return true
}

function openCleanup(): void {
  if (!certs.value.length) {
    ElMessage.info(t('certs.cleanupEmpty'))
    return
  }
  cleanupForm.scope = counts.value.expired > 0 ? 'expired' : certs.value.some((cert) => cert.status === 'revoked') ? 'revoked' : 'all'
  cleanupForm.expired_days = 0
  cleanupForm.revoked_days = 0
  cleanupForm.older_than_days = 0
  cleanupForm.domain = ''
  cleanupForm.ca = ''
  cleanupForm.source = ''
  cleanupForm.keep_latest_per_task = false
  cleanupVisible.value = true
}

async function submitCleanup(): Promise<void> {
  const count = cleanupCount.value
  if (!count) {
    ElMessage.info(t('certs.cleanupEmpty'))
    return
  }
  cleaning.value = true
  try {
    const data = await withSecondaryVerify<{ deleted: number }>('certificate_write', (headers) =>
      api.post<{ deleted: number }>('/certificates/cleanup', { ...cleanupForm }, headers)
    )
    if (data === null) return
    ElMessage.success(t('certs.cleanupDone', { count: data?.deleted ?? count }))
    cleanupVisible.value = false
    detailVisible.value = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    cleaning.value = false
  }
}

async function remove(cert: Certificate): Promise<void> {
  try {
    await ElMessageBox.confirm(t('certs.deleteConfirm', { domain: cert.primary_domain }), t('certs.deleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    const deleted = await withSecondaryVerify('certificate_write', (headers) => api.delete(`/certificates/${cert.id}`, headers))
    if (deleted === null) return
    ElMessage.success(t('certs.deleted'))
    if (detail.value?.id === cert.id) detailVisible.value = false
    load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function onSelectionChange(rows: Certificate[]): void {
  selectedCerts.value = rows
}

function clearSelection(): void {
  tableRef.value?.clearSelection()
  selectedCerts.value = []
}

async function batchDownload(): Promise<void> {
  const list = [...selectedCerts.value]
  if (!list.length) return
  batchDownloading.value = true
  let failed = 0
  try {
    for (const cert of list) {
      // 浏览器可能拦截连续下载，逐张触发即可，失败项单独计数。
      if (!(await download(cert, 'fullchain'))) failed++
    }
  } finally {
    batchDownloading.value = false
  }
  if (!failed) ElMessage.success(t('certs.batchDownloadDone', { count: list.length }))
  else ElMessage.warning(t('certs.batchDownloadFailed', { count: failed }))
}

async function batchRemove(): Promise<void> {
  const list = [...selectedCerts.value]
  if (!list.length) return
  try {
    await ElMessageBox.confirm(t('certs.batchDeleteConfirm', { count: list.length }), t('certs.batchDeleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  const token = await requestSecondaryVerification('certificate_write')
  if (token === null) return
  const verifyHeaders: Record<string, string> = token ? { [secondaryVerifyHeader]: token } : {}
  batchDeleting.value = true
  let failed = 0
  try {
    for (const cert of list) {
      try {
        await api.delete(`/certificates/${cert.id}`, verifyHeaders)
        if (detail.value?.id === cert.id) detailVisible.value = false
      } catch (error: any) {
        failed++
        ElMessage.error(`${cert.primary_domain}: ${error.message}`)
      }
    }
  } finally {
    batchDeleting.value = false
  }
  if (failed) ElMessage.warning(t('certs.batchDeleteFailed', { count: failed }))
  else ElMessage.success(t('certs.batchDeleted', { count: list.length }))
  clearSelection()
  await load()
}

onMounted(load)
</script>

<style scoped>
/* 外边框统一由 .k-card 负责，内部只保留分隔线，避免子元素补边造成断线和重线 */
.filter-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  border-bottom: 1px solid var(--kp-border);
}
.filter-row .muted {
  margin-right: 12px;
}
/* 分段按钮自身已有描边，不再叠加一条底线 */
.filter-row :deep(.el-radio-group) {
  border-bottom: 0;
}
.batch-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 10px 14px;
  border: 1px solid #dbe7fb;
  border-radius: 12px;
  background: #f5f9ff;
}
.batch-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.batch-actions .el-button + .el-button {
  margin-left: 0;
}
.batch-count {
  font-size: 13px;
  font-weight: 600;
  color: #1d4ed8;
}
/* 筛选行底边即表头分隔线 */
.cert-list :deep(.el-table__header-wrapper) {
  border-top: 0;
}
/* 表格自带的底边和最后一行边线会与卡片底边形成双线，这里清除 */
.cert-list :deep(.el-table__inner-wrapper::before) {
  display: none;
}
.cert-list :deep(.el-table__body tr:last-child > td.el-table__cell) {
  border-bottom: 0;
}
.detail-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 4px 0 8px;
  flex-wrap: wrap;
}
.analysis-box {
  display: flex;
  gap: 22px;
  align-items: center;
  padding: 20px;
  border: 1px solid #e6eaf2;
  border-radius: 16px;
  background: linear-gradient(135deg, #fbfdff, #f5f9ff);
}
.issue-list {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.issue-item {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  color: #b45309;
  background: #fffbeb;
  border-radius: 8px;
  padding: 7px 10px;
}
.issue-item.ok {
  color: #047857;
  background: #ecfdf5;
  margin-top: 12px;
}
.cleanup-form {
  margin-top: 18px;
}
.cleanup-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 18px;
}
.cleanup-grid :deep(.el-input-number),
.cleanup-grid :deep(.el-select) {
  width: 100%;
}
.cleanup-note {
  margin: -4px 0 10px;
  color: #94a3b8;
  font-size: 12.5px;
  line-height: 1.6;
}
.cleanup-preview {
  margin-top: 6px;
}
.deploy-target {
  border: 1px solid var(--kp-border);
  border-radius: 12px;
  padding: 14px 14px 2px;
  margin-bottom: 12px;
}
.deploy-target-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.deploy-results {
  margin-top: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.deploy-result {
  display: flex;
  gap: 9px;
  align-items: flex-start;
  background: #ecfdf5;
  color: #047857;
  border-radius: 10px;
  padding: 10px 12px;
  font-size: 12.5px;
}
.deploy-result.fail {
  background: #fef2f2;
  color: #b91c1c;
}
</style>
