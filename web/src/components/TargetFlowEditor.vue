<template>
  <div class="target-flow-editor">
    <section class="tf-card">
      <div class="tf-card-head">
        <div>
          <div class="tf-card-title">{{ t('tasks.betaTargets') }}</div>
          <div class="tf-card-sub">{{ t('tasks.betaTargetsHint') }}</div>
        </div>
        <el-button size="small" type="primary" plain :icon="Plus" @click="addSource">
          {{ t('tasks.betaAddTarget') }}
        </el-button>
      </div>

      <div v-if="!sources.length" class="tf-empty">
        {{ t('tasks.betaNoTargets') }}
      </div>

      <div v-for="(source, index) in sources" :key="source.deploy_account_id || index" class="tf-source">
        <div class="tf-source-head">
          <el-select
            :model-value="source.deploy_account_id || undefined"
            :placeholder="t('tasks.betaTargetPlaceholder')"
            style="width: 320px"
            filterable
            @update:model-value="(value: number) => changeSourceAccount(source, value)"
          >
            <el-option
              v-for="account in deployAccounts"
              :key="account.id"
              :label="`${account.name} · ${deployLabel(account.type)}`"
              :value="account.id"
            />
          </el-select>
          <span class="muted">{{ sourceSummary(source) }}</span>
          <el-button text type="danger" :icon="Delete" @click="removeSource(index)">{{ t('common.delete') }}</el-button>
        </div>
        <DeployConfigFields
          v-if="source.deploy_account_id"
          :account-type="accountTypeOf(source.deploy_account_id)"
          :account-id="source.deploy_account_id"
          :model-value="source.deploy_config || {}"
          :exclude-keys="siteField(source) ? [siteField(source)] : []"
          @update:model-value="(value: Record<string, string>) => updateSourceConfig(source, value)"
        />
        <div v-if="source.deploy_account_id && !siteField(source)" class="form-help warn">
          {{ t('tasks.betaTargetNotSite') }}
        </div>
      </div>
    </section>

    <section class="tf-card">
      <div class="tf-card-head">
        <div>
          <div class="tf-card-title">{{ t('tasks.betaSites') }}</div>
          <div class="tf-card-sub">{{ t('tasks.betaSitesHint') }}</div>
        </div>
        <div class="flex gap-10 wrap">
          <el-button size="small" :icon="Refresh" :loading="loading" @click="refreshPreview">
            {{ t('tasks.betaReadSites') }}
          </el-button>
          <el-button size="small" text @click="selectAll">{{ t('tasks.betaSelectAll') }}</el-button>
          <el-button size="small" text @click="clearSelection">{{ t('tasks.betaClearSelection') }}</el-button>
        </div>
      </div>

      <el-radio-group v-model="selectionMode" size="small" style="margin-bottom: 12px" @change="onSelectionModeChange">
        <el-radio-button value="all">{{ t('tasks.betaSelectionAll') }}</el-radio-button>
        <el-radio-button value="selected">{{ t('tasks.betaSelectionSelected') }}</el-radio-button>
      </el-radio-group>

      <div class="tf-filters">
        <el-input v-model="includeText" size="small" :placeholder="t('tasks.betaIncludeDomains')" @change="applyDomainFilters">
          <template #prepend>{{ t('tasks.betaInclude') }}</template>
        </el-input>
        <el-input v-model="excludeText" size="small" :placeholder="t('tasks.betaExcludeDomains')" @change="applyDomainFilters">
          <template #prepend>{{ t('tasks.betaExclude') }}</template>
        </el-input>
      </div>

      <div v-if="previewError" class="tf-error">
        <span>{{ previewError }}</span>
        <el-button link type="primary" size="small" @click="refreshPreview">{{ t('tasks.betaRetry') }}</el-button>
      </div>
      <div v-else-if="!sites.length" class="tf-empty">
        {{ loading ? t('tasks.betaReading') : t('tasks.betaNoSites') }}
      </div>

      <div v-else class="tf-site-wrap">
        <div class="tf-site-summary">
          <span>{{ t('tasks.betaSiteCount', { selected: selectedCount, total: sites.length }) }}</span>
          <span v-if="stale" class="tf-stale">{{ t('tasks.betaPreviewStale') }}</span>
        </div>
        <el-table :data="sites" size="small" max-height="310" row-key="key" style="width: 100%">
          <el-table-column width="52">
            <template #default="{ row }">
              <el-checkbox
                :model-value="selectedKeys.includes(row.key)"
                :disabled="row.status === 'invalid'"
                @change="() => toggleSite(row)"
              />
            </template>
          </el-table-column>
          <el-table-column min-width="170" :label="t('tasks.betaSite')">
            <template #default="{ row }">
              <div class="tf-site-name">{{ row.name || row.value }}</div>
              <div class="tf-site-value">{{ row.value }}</div>
            </template>
          </el-table-column>
          <el-table-column min-width="220" :label="t('tasks.betaSiteDomains')">
            <template #default="{ row }">
              <div class="tf-domains">
                <el-tag v-for="domain in row.domains" :key="domain" size="small" effect="plain">{{ domain }}</el-tag>
                <span v-if="!row.domains.length" class="muted">—</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column width="130" :label="t('tasks.betaSiteSource')">
            <template #default="{ row }">
              <span class="muted">{{ row.account_name || row.account_id }}</span>
            </template>
          </el-table-column>
          <el-table-column width="110" :label="t('tasks.betaSiteStatus')">
            <template #default="{ row }">
              <el-tooltip :content="(row.warnings || []).join('；')" :disabled="!row.warnings?.length" placement="top">
                <el-tag :type="siteStatusType(row.status)" size="small" effect="light">
                  {{ siteStatusLabel(row.status) }}
                </el-tag>
              </el-tooltip>
            </template>
          </el-table-column>
          <el-table-column v-if="isCustom" width="170" :label="t('tasks.betaManualGroup')">
            <template #default="{ row }">
              <el-input
                v-model="manualGroups[row.key]"
                size="small"
                :placeholder="row.name || row.value"
                :disabled="!selectedKeys.includes(row.key)"
                @change="markStale"
              />
            </template>
          </el-table-column>
        </el-table>
      </div>
    </section>

    <section class="tf-card">
      <div class="tf-card-head">
        <div>
          <div class="tf-card-title">{{ t('tasks.betaStrategy') }}</div>
          <div class="tf-card-sub">{{ t('tasks.betaStrategyHint') }}</div>
        </div>
        <div class="flex gap-10" style="align-items: center">
          <span class="muted">{{ t('tasks.betaMaxDomains') }}</span>
          <el-input-number v-model="maxDomains" :min="1" :max="500" size="small" controls-position="right" style="width: 110px" @change="markStale" />
        </div>
      </div>

      <div class="tf-strategies">
        <button
          v-for="item in strategies"
          :key="item.value"
          type="button"
          class="tf-strategy"
          :class="{ active: config.policy.strategy === item.value }"
          @click="selectStrategy(item.value)"
        >
          <span class="tf-strategy-title">{{ t(item.label) }}</span>
          <span class="tf-strategy-desc">{{ t(item.desc) }}</span>
        </button>
      </div>

      <el-alert
        v-if="recommendation"
        type="info"
        :closable="false"
        class="tf-recommendation"
        :title="t('tasks.betaRecommendation', { strategy: strategyLabel(recommendation.strategy) })"
      >
        <div v-for="reason in recommendation.reasons || []" :key="reason" class="tf-reason">· {{ reason }}</div>
        <el-button
          v-if="recommendation.strategy !== config.policy.strategy"
          link
          type="primary"
          size="small"
          @click="applyRecommendation"
        >
          {{ t('tasks.betaApplyRecommendation') }}
        </el-button>
      </el-alert>
    </section>

    <section v-if="groups.length || warnings.length" class="tf-card">
      <div class="tf-card-head">
        <div>
          <div class="tf-card-title">{{ t('tasks.betaGroups') }}</div>
          <div class="tf-card-sub">
            {{ t('tasks.betaGroupSummary', { groups: groups.length, sites: selectedCount }) }}
            <span v-if="stale" class="tf-stale">{{ t('tasks.betaPreviewStale') }}</span>
          </div>
        </div>
        <el-button size="small" :icon="Refresh" :loading="loading" @click="refreshPreview">
          {{ t('tasks.betaRefreshPreview') }}
        </el-button>
      </div>

      <el-alert
        v-for="warning in warnings"
        :key="warning"
        type="warning"
        :closable="false"
        :title="warning"
        class="tf-warning"
      />

      <div v-if="!groups.length" class="tf-empty">{{ t('tasks.betaPreviewEmpty') }}</div>
      <div v-else class="tf-groups">
        <div v-for="group in groups" :key="group.key" class="tf-group">
          <div class="tf-group-head">
            <div class="tf-group-title">{{ group.label }}</div>
            <div class="flex gap-10 wrap">
              <el-tag size="small" effect="plain">{{ t('tasks.betaGroupDomainCount', { count: group.domains.length }) }}</el-tag>
              <el-tag size="small" effect="plain">{{ t('tasks.betaGroupSiteCount', { count: group.sites.length }) }}</el-tag>
              <el-tag v-if="group.reuse_cert_id" size="small" type="success" effect="light">
                {{ t('tasks.betaReuseCert', { id: group.reuse_cert_id }) }}
              </el-tag>
            </div>
          </div>
          <div class="tf-domains">
            <el-tag v-for="domain in group.domains" :key="domain" size="small" effect="plain">{{ domain }}</el-tag>
          </div>
          <div class="tf-group-sites">
            <span class="muted">{{ t('tasks.betaGroupSites') }}</span>
            <span v-for="site in group.sites" :key="site.key" class="tf-site-chip">{{ site.name || site.value }}</span>
          </div>
          <div class="tf-extra">
            <span class="muted">{{ t('tasks.betaExtraDomains') }}</span>
            <el-input
              v-model="extraText[group.key]"
              size="small"
              :placeholder="t('tasks.betaExtraDomainsPlaceholder')"
              @change="onExtraChange(group)"
            />
          </div>
          <div v-for="warning in group.warnings || []" :key="warning" class="form-help warn">{{ warning }}</div>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Delete, Plus, Refresh } from '@element-plus/icons-vue'
import { api, errorText } from '../api'
import DeployConfigFields from './DeployConfigFields.vue'
import { deployLabel, normalizeDeployConfig, siteFieldOf } from '../deployConfig'
import type {
  CertGroup,
  CertPolicy,
  CertStrategy,
  DeployAccount,
  SiteSelectionMode,
  StrategyRecommendation,
  TargetFlowConfig,
  TargetFlowPreview,
  TargetSite
} from '../types'

const config = defineModel<TargetFlowConfig>({ required: true })
const props = defineProps<{
  deployAccounts: DeployAccount[]
  publicCa: boolean
}>()

const { t } = useI18n()

const sources = computed(() => config.value.sources || [])
const sites = ref<TargetSite[]>([])
const groups = ref<CertGroup[]>([])
const recommendation = ref<StrategyRecommendation | null>(null)
const warnings = ref<string[]>([])
const selectedKeys = ref<string[]>([])
const manualGroups = reactive<Record<string, string>>({})
const extraText = reactive<Record<string, string>>({})
const loading = ref(false)
const previewError = ref('')
const stale = ref(false)
const includeText = ref((config.value.selection?.include_domains || []).join('\n'))
const excludeText = ref((config.value.selection?.exclude_domains || []).join('\n'))

const strategies: Array<{ value: CertStrategy; label: string; desc: string }> = [
  { value: 'smart', label: 'tasks.betaStrategySmart', desc: 'tasks.betaStrategySmartDesc' },
  { value: 'per_site', label: 'tasks.betaStrategyPerSite', desc: 'tasks.betaStrategyPerSiteDesc' },
  { value: 'per_base_domain', label: 'tasks.betaStrategyPerBaseDomain', desc: 'tasks.betaStrategyPerBaseDomainDesc' },
  { value: 'single', label: 'tasks.betaStrategySingle', desc: 'tasks.betaStrategySingleDesc' },
  { value: 'per_source', label: 'tasks.betaStrategyPerSource', desc: 'tasks.betaStrategyPerSourceDesc' },
  { value: 'custom', label: 'tasks.betaStrategyCustom', desc: 'tasks.betaStrategyCustomDesc' }
]

const selectionMode = computed<SiteSelectionMode>({
  get: () => (config.value.selection?.mode === 'selected' ? 'selected' : 'all'),
  set: (value) => {
    config.value.selection = { ...config.value.selection, mode: value }
  }
})
const maxDomains = computed<number>({
  get: () => config.value.policy?.max_domains_per_cert || 100,
  set: (value) => {
    config.value.policy = { ...config.value.policy, max_domains_per_cert: value || 100 }
  }
})
const selectedCount = computed(() => sites.value.filter((site) => selectedKeys.value.includes(site.key) && site.status !== 'invalid').length)
const isCustom = computed(() => config.value.policy?.strategy === 'custom')

watch(
  () => config.value.sources,
  () => {
    previewError.value = ''
    stale.value = true
  },
  { deep: true }
)

// 自定义分组时，把站点与组名的映射同步回任务配置，保存时无需再刷新预览。
watch(
  [selectedKeys, () => JSON.stringify(manualGroups), () => config.value.policy?.strategy],
  () => {
    if (isCustom.value) config.value.group_overrides = buildOverrides()
  },
  { deep: true }
)

function accountOf(id: number | undefined): DeployAccount | undefined {
  return props.deployAccounts.find((item) => item.id === id)
}

function accountTypeOf(id: number | undefined): string {
  return accountOf(id)?.type || ''
}

function siteField(source: { deploy_account_id: number; deploy_config?: Record<string, string> }): string {
  return siteFieldOf(accountOf(source.deploy_account_id), source.deploy_config || {})
}

function sourceSummary(source: { deploy_account_id: number; deploy_config?: Record<string, string> }): string {
  const account = accountOf(source.deploy_account_id)
  if (!account) return ''
  const field = siteField(source)
  return field ? `${deployLabel(account.type)} · ${field}` : deployLabel(account.type)
}

function addSource(): void {
  const used = new Set(sources.value.map((item) => item.deploy_account_id))
  const account = props.deployAccounts.find((item) => !used.has(item.id))
  if (!account) return
  const cfg = normalizeDeployConfig(account.type, account.config)
  const field = siteFieldOf(account, cfg)
  if (field) delete cfg[field]
  config.value.sources = [...sources.value, { deploy_account_id: account.id, deploy_config: cfg }]
  markStale()
}

function changeSourceAccount(source: { deploy_account_id: number; deploy_config: Record<string, string> }, accountID: number): void {
  const account = accountOf(accountID)
  if (!account) return
  const cfg = normalizeDeployConfig(account.type, account.config)
  const field = siteFieldOf(account, cfg)
  if (field) delete cfg[field]
  source.deploy_account_id = accountID
  source.deploy_config = cfg
  markStale()
}

function updateSourceConfig(source: { deploy_config: Record<string, string> }, value: Record<string, string>): void {
  source.deploy_config = value
  markStale()
}

function removeSource(index: number): void {
  config.value.sources = sources.value.filter((_, itemIndex) => itemIndex !== index)
  markStale()
}

function markStale(): void {
  stale.value = true
}

function splitDomainText(text: string): string[] {
  return text
    .split(/[\n,;]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function applyDomainFilters(): void {
  config.value.selection = {
    ...config.value.selection,
    include_domains: splitDomainText(includeText.value),
    exclude_domains: splitDomainText(excludeText.value)
  }
  markStale()
}

function onSelectionModeChange(mode: SiteSelectionMode): void {
  if (mode === 'all') {
    selectedKeys.value = sites.value.filter((site) => site.status !== 'invalid').map((site) => site.key)
    config.value.selection.keys = []
  } else {
    config.value.selection.keys = [...selectedKeys.value]
  }
  markStale()
}

function toggleSite(site: TargetSite): void {
  if (selectedKeys.value.includes(site.key)) {
    selectedKeys.value = selectedKeys.value.filter((key) => key !== site.key)
  } else {
    selectedKeys.value = [...selectedKeys.value, site.key]
  }
  selectionMode.value = 'selected'
  config.value.selection.keys = [...selectedKeys.value]
  markStale()
}

function selectAll(): void {
  selectedKeys.value = sites.value.filter((site) => site.status !== 'invalid').map((site) => site.key)
  selectionMode.value = 'all'
  config.value.selection.keys = []
  markStale()
}

function clearSelection(): void {
  selectedKeys.value = []
  selectionMode.value = 'selected'
  config.value.selection.keys = []
  markStale()
}

function selectStrategy(value: CertStrategy): void {
  config.value.policy = { ...config.value.policy, strategy: value }
  if (value === 'custom') {
    for (const site of sites.value) {
      if (!manualGroups[site.key]) manualGroups[site.key] = site.name || site.value
    }
  }
  void refreshPreview()
}

function applyRecommendation(): void {
  if (!recommendation.value) return
  selectStrategy(recommendation.value.strategy)
}

function strategyLabel(value: string): string {
  const item = strategies.find((entry) => entry.value === value)
  return item ? t(item.label) : value
}

function siteStatusType(status: string): 'success' | 'warning' | 'info' | 'danger' {
  if (status === 'ready') return 'success'
  if (status === 'covered') return 'success'
  if (status === 'duplicate') return 'warning'
  if (status === 'invalid') return 'danger'
  return 'info'
}

function siteStatusLabel(status: string): string {
  const map: Record<string, string> = {
    ready: 'tasks.betaStatusReady',
    covered: 'tasks.betaStatusCovered',
    duplicate: 'tasks.betaStatusDuplicate',
    invalid: 'tasks.betaStatusInvalid',
    skipped: 'tasks.betaStatusSkipped'
  }
  return t(map[status] || status)
}

function buildOverrides(): Array<{ key: string; label: string; sites: string[] }> {
  const buckets = new Map<string, string[]>()
  for (const key of selectedKeys.value) {
    const site = sites.value.find((item) => item.key === key)
    if (!site) continue
    const label = (manualGroups[key] || site.name || site.value || key).trim()
    const list = buckets.get(label) || []
    list.push(key)
    buckets.set(label, list)
  }
  return [...buckets.entries()].map(([label, keys]) => ({
    key: `custom:${label}`,
    label,
    sites: keys
  }))
}

async function refreshPreview(): Promise<void> {
  if (!sources.value.length) {
    previewError.value = t('tasks.betaNoTargets')
    return
  }
  loading.value = true
  previewError.value = ''
  try {
    const payload = {
      sources: sources.value.map((source) => ({
        deploy_account_id: source.deploy_account_id,
        deploy_config: source.deploy_config || {}
      })),
      selection: {
        mode: config.value.selection?.mode || 'all',
        keys: selectedKeys.value,
        include_domains: config.value.selection?.include_domains || [],
        exclude_domains: config.value.selection?.exclude_domains || []
      },
      policy: {
        strategy: config.value.policy?.strategy || 'smart',
        max_domains_per_cert: maxDomains.value,
        extra_domains: config.value.policy?.extra_domains || {}
      },
      group_overrides: isCustom.value ? buildOverrides() : undefined,
      public_ca: props.publicCa
    }
    if (isCustom.value) {
      config.value.group_overrides = buildOverrides()
    }
    const data = await api.post<TargetFlowPreview>('/tasks/target-flow/preview', payload)
    if (!data) return
    sites.value = data.sites || []
    groups.value = data.groups || []
    recommendation.value = data.recommendation || null
    warnings.value = data.warnings || []
    if (selectionMode.value === 'selected') {
      const available = new Set(sites.value.map((site) => site.key))
      const configured = config.value.selection.keys || []
      selectedKeys.value = configured.filter((key) => available.has(key))
      if (!selectedKeys.value.length) {
        selectedKeys.value = sites.value.filter((site) => site.selected).map((site) => site.key)
      }
    } else {
      selectedKeys.value = sites.value.filter((site) => site.selected).map((site) => site.key)
    }
    config.value.selection.keys = selectionMode.value === 'selected' ? [...selectedKeys.value] : []
    for (const group of groups.value) {
      if (extraText[group.key] === undefined) {
        extraText[group.key] = (config.value.policy?.extra_domains?.[group.key] || []).join('\n')
      }
    }
    if (isCustom.value) {
      for (const group of groups.value) {
        for (const site of group.sites) {
          manualGroups[site.key] = group.label
        }
      }
    }
    stale.value = false
  } catch (error: any) {
    previewError.value = errorText(error)
  } finally {
    loading.value = false
  }
}

function onExtraChange(group: CertGroup): void {
  const text = extraText[group.key] || ''
  const domains = text
    .split(/[\n,;]/)
    .map((item) => item.trim())
    .filter(Boolean)
  const next: CertPolicy = { ...(config.value.policy || { strategy: 'smart' }) }
  next.extra_domains = { ...(next.extra_domains || {}), [group.key]: domains }
  config.value.policy = next
  markStale()
}
</script>

<style scoped>
.target-flow-editor {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.tf-card {
  border: 1px solid var(--kp-border);
  border-radius: 12px;
  padding: 14px 16px;
  background: #fff;
}
.tf-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.tf-card-title {
  font-size: 14px;
  font-weight: 700;
  color: #0f172a;
}
.tf-card-sub {
  margin-top: 3px;
  font-size: 12px;
  color: #64748b;
}
.tf-empty {
  padding: 22px 0;
  text-align: center;
  color: #94a3b8;
  font-size: 13px;
}
.tf-source {
  border: 1px dashed #dbe4f0;
  border-radius: 10px;
  padding: 12px;
  margin-top: 10px;
  background: #f8fbff;
}
.tf-source-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.tf-filters {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 8px;
  margin-bottom: 12px;
}
.tf-error {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 10px 12px;
  border-radius: 8px;
  background: #fff7ed;
  color: #b45309;
  font-size: 13px;
}
.tf-site-wrap {
  border: 1px solid var(--kp-border);
  border-radius: 10px;
  overflow: hidden;
}
.tf-site-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 12px;
  font-size: 12px;
  color: #64748b;
  background: #f8fafc;
  border-bottom: 1px solid var(--kp-border);
}
.tf-stale {
  color: #c2410c;
  font-weight: 600;
}
.tf-site-name {
  font-weight: 600;
  color: #0f172a;
}
.tf-site-value {
  font-size: 11px;
  color: #94a3b8;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.tf-domains {
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
}
.tf-strategies {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px;
}
.tf-strategy {
  text-align: left;
  border: 1px solid #dbe4f0;
  border-radius: 10px;
  padding: 10px 12px;
  background: #fff;
  cursor: pointer;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.tf-strategy:hover {
  border-color: #93c5fd;
}
.tf-strategy.active {
  border-color: #2563eb;
  box-shadow: 0 0 0 2px rgba(37, 99, 235, 0.12);
  background: #f5f9ff;
}
.tf-strategy-title {
  display: block;
  font-size: 13px;
  font-weight: 700;
  color: #0f172a;
}
.tf-strategy-desc {
  display: block;
  margin-top: 4px;
  font-size: 11px;
  line-height: 1.5;
  color: #64748b;
}
.tf-recommendation {
  margin-top: 12px;
}
.tf-reason {
  font-size: 12px;
  line-height: 1.6;
}
.tf-warning {
  margin-bottom: 8px;
}
.tf-groups {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.tf-group {
  border: 1px solid #dbe4f0;
  border-radius: 10px;
  padding: 12px;
  background: #fbfdff;
}
.tf-group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.tf-group-title {
  font-size: 13px;
  font-weight: 700;
  color: #0f172a;
}
.tf-group-sites {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 8px;
  font-size: 12px;
}
.tf-site-chip {
  display: inline-block;
  padding: 2px 7px;
  border-radius: 999px;
  background: #eef6ff;
  color: #1d4ed8;
}
.tf-extra {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
  font-size: 12px;
}
.tf-extra :deep(.el-input) {
  max-width: 420px;
}
</style>
