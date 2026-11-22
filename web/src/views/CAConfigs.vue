<template>
  <div class="page table-page ca-page">
    <div class="page-head">
      <div>
        <h1>{{ t('ca.title') }}</h1>
        <p>{{ t('ca.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle :loading="loading" @click="load" />
      </div>
    </div>

    <div class="k-card ca-card table-fill" v-loading="loading" style="padding: 0; overflow: hidden">
      <el-tabs v-model="tab" class="ca-tabs">
        <el-tab-pane :label="t('ca.tabs.upstreams')" name="upstreams">
          <div class="ca-notice">
            <el-icon><InfoFilled /></el-icon>
            <span>{{ t('ca.alert') }}</span>
          </div>

          <div class="filter-row">
            <el-radio-group v-model="upstreamFilter" size="default">
              <el-radio-button value="all">{{ t('ca.filterAll', { count: upstreamRows.length }) }}</el-radio-button>
              <el-radio-button value="builtin">{{ t('ca.filterBuiltin', { count: builtinCount }) }}</el-radio-button>
              <el-radio-button value="custom">{{ t('ca.filterCustom', { count: customCount }) }}</el-radio-button>
            </el-radio-group>
            <el-input v-model="upstreamKeyword" clearable :placeholder="t('ca.searchPlaceholder')" style="width: 240px">
              <template #prefix><el-icon><Search /></el-icon></template>
            </el-input>
            <span class="filter-spacer" />
            <el-button type="primary" :icon="Plus" @click="openGenericCreate">{{ t('ca.genericAdd') }}</el-button>
          </div>

          <div class="table-scroll">
            <el-table :data="filteredUpstreams" height="100%" style="width: 100%">
              <el-table-column min-width="260" :label="t('ca.ca')">
                <template #default="{ row }">
                  <div class="ca-title">
                    <span class="ca-name">{{ row.label }}</span>
                    <span class="tag-soft tag-gray">{{ row.source === 'builtin' ? t('ca.sourceBuiltin') : t('ca.sourceCustom') }}</span>
                    <span v-if="row.isDefault" class="tag-soft tag-blue">{{ t('ca.default') }}</span>
                  </div>
                  <div class="ca-desc">{{ row.desc || t('ca.genericTitle') }}</div>
                </template>
              </el-table-column>
              <el-table-column width="76" :label="t('ca.id')">
                <template #default="{ row }">
                  <span v-if="row.id != null" class="mono">{{ row.id }}</span>
                  <span v-else class="muted">—</span>
                </template>
              </el-table-column>
              <el-table-column width="90" :label="t('ca.protocol')">
                <template #default="{ row }">
                  <span class="tag-soft tag-gray">{{ String(row.protocol).toUpperCase() }}</span>
                </template>
              </el-table-column>
              <el-table-column min-width="240" :label="t('ca.directory')">
                <template #default="{ row }">
                  <el-tooltip :content="row.url" placement="top" :disabled="!row.url">
                    <span class="ca-url">{{ row.url }}</span>
                  </el-tooltip>
                </template>
              </el-table-column>
              <el-table-column width="120" :label="t('ca.eab')">
                <template #default="{ row }">
                  <span v-if="!row.eabRequired" class="tag-soft tag-gray">{{ t('ca.eabNotRequired') }}</span>
                  <el-tag v-else-if="row.eabConfigured" type="success" effect="light" round size="small">
                    {{ t('ca.eabConfigured') }}
                  </el-tag>
                  <el-tag v-else type="warning" effect="light" round size="small">{{ t('ca.eabNotConfigured') }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column width="170" fixed="right" :label="t('ca.actions')">
                <template #default="{ row }">
                  <template v-if="row.source === 'builtin'">
                    <el-button v-if="row.eabRequired" text type="primary" size="small" @click="openBuiltinEAB(row)">
                      {{ t('ca.configureEAB') }}
                    </el-button>
                    <span v-else class="muted">{{ t('ca.noConfigNeeded') }}</span>
                  </template>
                  <template v-else>
                    <el-button text type="primary" size="small" @click="editCustomUpstream(row)">{{ t('ca.genericEdit') }}</el-button>
                    <el-dropdown trigger="click" @command="(cmd: string) => handleUpstreamCommand(cmd, row)">
                      <el-button text size="small">
                        {{ t('common.more') }}<el-icon class="el-icon--right"><ArrowDown /></el-icon>
                      </el-button>
                      <template #dropdown>
                        <el-dropdown-menu>
                          <el-dropdown-item command="delete" divided>{{ t('ca.genericDelete') }}</el-dropdown-item>
                        </el-dropdown-menu>
                      </template>
                    </el-dropdown>
                  </template>
                </template>
              </el-table-column>
              <template #empty>
                <div class="ca-empty">
                  {{ upstreamKeyword ? t('ca.noMatchingUpstream') : t('ca.genericEmpty') }}
                </div>
              </template>
            </el-table>
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('ca.tabs.pools')" name="pools">
          <div class="filter-row">
            <span class="muted">{{ t('ca.poolCount', { count: pools.length }) }}</span>
            <span class="filter-spacer" />
            <el-button type="primary" :icon="Plus" @click="openPoolCreate">{{ t('ca.poolAdd') }}</el-button>
          </div>

          <div class="table-scroll">
            <el-table :data="pools" height="100%" style="width: 100%">
              <el-table-column min-width="200" :label="t('ca.poolName')">
                <template #default="{ row }">
                  <div class="ca-title">
                    <span class="ca-name">{{ row.name }}</span>
                    <span v-if="mode === 'pool' && acme.acme_ca_pool_id === row.id" class="tag-soft tag-blue">{{ t('ca.default') }}</span>
                    <span v-if="!row.enabled" class="tag-soft tag-gray">{{ t('common.disabled') }}</span>
                  </div>
                </template>
              </el-table-column>
              <el-table-column width="120" :label="t('ca.poolStrategy')">
                <template #default="{ row }">
                  <span class="tag-soft tag-gray">{{ strategyLabel(row.strategy) }}</span>
                </template>
              </el-table-column>
              <el-table-column min-width="280" :label="t('ca.poolCandidates')">
                <template #default="{ row }">
                  <el-tooltip :content="poolTooltip(row)" placement="top" :disabled="!enabledCandidates(row).length">
                    <div class="ca-chips">
                      <span v-for="(label, index) in poolChips(row)" :key="index" class="tag-soft tag-gray">{{ label }}</span>
                      <span v-if="poolHiddenCount(row) > 0" class="tag-soft tag-gray">+{{ poolHiddenCount(row) }}</span>
                      <span v-if="!enabledCandidates(row).length" class="muted">{{ t('ca.poolNoCandidates') }}</span>
                    </div>
                  </el-tooltip>
                </template>
              </el-table-column>
              <el-table-column width="150" fixed="right" :label="t('ca.actions')">
                <template #default="{ row }">
                  <el-button text type="primary" size="small" @click="openPoolEdit(row)">{{ t('common.edit') }}</el-button>
                  <el-dropdown trigger="click" @command="(cmd: string) => handlePoolCommand(cmd, row)">
                    <el-button text size="small">
                      {{ t('common.more') }}<el-icon class="el-icon--right"><ArrowDown /></el-icon>
                    </el-button>
                    <template #dropdown>
                      <el-dropdown-menu>
                        <el-dropdown-item command="delete" divided>{{ t('common.delete') }}</el-dropdown-item>
                      </el-dropdown-menu>
                    </template>
                  </el-dropdown>
                </template>
              </el-table-column>
              <template #empty>
                <div class="ca-empty">{{ t('ca.poolEmpty') }}</div>
              </template>
            </el-table>
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('ca.tabs.defaults')" name="defaults">
          <div class="ca-form-scroll">
            <div class="ca-effective" :class="{ 'is-warning': mode === 'pool' && !defaultPoolActive }">
              <el-icon><InfoFilled /></el-icon>
              <span>{{ effectiveSummary }}</span>
            </div>

            <el-form :model="acme" label-position="top" class="ca-default-form">
              <div class="grid grid-2" style="gap: 0 18px">
                <el-form-item :label="t('ca.acmeEmail')">
                  <el-input v-model="acme.acme_email" placeholder="name@example.com" />
                  <div class="form-help">{{ t('ca.acmeEmailHelp') }}</div>
                </el-form-item>
                <el-form-item :label="t('ca.defaultKeyType')">
                  <el-select v-model="acme.acme_key_type" style="width: 100%">
                    <el-option v-for="item in meta?.key_types || []" :key="item.value" :label="item.label" :value="item.value" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('ca.renewDays')">
                  <el-input-number v-model="acme.acme_renew_days" :min="1" :max="60" style="width: 100%" />
                </el-form-item>
              </div>

              <div class="ca-mode">
                <div class="ca-mode-title">{{ t('ca.defaultMode') }}</div>
                <el-radio-group v-model="mode" size="default">
                  <el-radio-button value="single">{{ t('ca.defaultModeSingle') }}</el-radio-button>
                  <el-radio-button value="pool">{{ t('ca.defaultModePool') }}</el-radio-button>
                </el-radio-group>

                <div class="grid grid-2 ca-mode-fields">
                  <el-form-item v-if="mode === 'single'" :label="t('ca.defaultCA')">
                    <el-select v-model="acme.acme_ca" style="width: 100%">
                      <el-option v-for="ca in meta?.cas || []" :key="ca.value" :label="ca.label" :value="ca.value" />
                    </el-select>
                    <div class="form-help">{{ t('ca.defaultCAHelp') }}</div>
                  </el-form-item>
                  <template v-else>
                    <el-form-item :label="t('ca.defaultPool')">
                      <el-select v-model="acme.acme_ca_pool_id" style="width: 100%">
                        <el-option
                          v-for="pool in pools"
                          :key="pool.id"
                          :label="pool.enabled ? pool.name : `${pool.name} · ${t('common.disabled')}`"
                          :value="pool.id"
                          :disabled="!pool.enabled"
                        />
                      </el-select>
                      <div class="form-help">{{ t('ca.defaultPoolHelp') }}</div>
                    </el-form-item>
                    <el-form-item :label="t('ca.fallbackCA')">
                      <el-select v-model="acme.acme_ca" style="width: 100%">
                        <el-option v-for="ca in meta?.cas || []" :key="ca.value" :label="ca.label" :value="ca.value" />
                      </el-select>
                    </el-form-item>
                  </template>
                </div>
                <div class="form-help">{{ t('ca.defaultModeHelp') }}</div>
              </div>
            </el-form>
          </div>

          <div class="ca-form-footer">
            <span class="muted">{{ t('ca.defaultParamsSub') }}</span>
            <el-button type="primary" :loading="savingACME" @click="saveACME">{{ t('ca.saveCerts') }}</el-button>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>

    <el-dialog
      v-model="poolDialogVisible"
      :title="poolEditingId ? t('ca.poolEditTitle') : t('ca.poolAddTitle')"
      width="680px"
      destroy-on-close
    >
      <el-form :model="poolForm" label-position="top">
        <div class="grid grid-2" style="gap: 0 16px">
          <el-form-item :label="t('ca.poolName')" required>
            <el-input v-model="poolForm.name" :placeholder="t('ca.poolNamePlaceholder')" maxlength="80" />
          </el-form-item>
          <el-form-item :label="t('ca.poolStrategy')">
            <el-select v-model="poolForm.strategy" style="width: 100%">
              <el-option :label="t('ca.strategyRoundRobin')" value="round_robin" />
              <el-option :label="t('ca.strategyRandom')" value="random" />
              <el-option :label="t('ca.strategyPriority')" value="priority" />
            </el-select>
          </el-form-item>
        </div>
        <el-form-item :label="t('common.enabled')">
          <el-switch v-model="poolForm.enabled" />
        </el-form-item>
        <el-form-item :label="t('ca.poolCandidates')">
          <div style="width: 100%">
            <div v-for="(item, index) in poolForm.items" :key="index" class="pool-item">
              <el-select v-model="item.ca_value" filterable style="flex: 1" :placeholder="t('ca.poolSelectCA')">
                <el-option v-for="ca in poolCAOptions(index)" :key="ca.value" :label="ca.label" :value="ca.value" />
              </el-select>
              <el-input-number v-model="item.priority" :min="0" :max="9999" controls-position="right" style="width: 120px" />
              <el-input-number v-model="item.weight" :min="1" :max="100" controls-position="right" style="width: 110px" />
              <el-switch v-model="item.enabled" />
              <el-button text type="danger" @click="removePoolItem(index)">{{ t('common.delete') }}</el-button>
            </div>
            <el-button text type="primary" :icon="Plus" @click="addPoolItem">{{ t('ca.poolAddCandidate') }}</el-button>
            <div class="form-help">{{ t('ca.poolCandidateHelp') }}</div>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="poolDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="poolSaving" @click="savePool">{{ t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="genericDialogVisible"
      :title="genericEditingId ? t('ca.genericEditTitle') : t('ca.genericAddTitle')"
      width="540px"
      destroy-on-close
    >
      <el-form :model="genericForm" label-position="top">
        <el-form-item :label="t('ca.genericName')" required>
          <el-input v-model="genericForm.name" :placeholder="t('ca.genericNamePlaceholder')" maxlength="80" />
        </el-form-item>
        <el-form-item :label="t('ca.genericURL')" required>
          <el-input v-model="genericForm.directory_url" :placeholder="t('ca.genericURLPlaceholder')" />
          <div class="form-help">{{ t('ca.genericURLHelp') }}</div>
        </el-form-item>
        <el-form-item :label="t('ca.genericProtocol')" required>
          <el-select v-model="genericForm.protocol" style="width: 100%" @change="onGenericProtocolChange">
            <el-option label="ACME v2 (RFC 8555)" value="v2" />
            <el-option label="ACME v1 (legacy)" value="v1" />
          </el-select>
          <div class="form-help">{{ t('ca.genericProtocolHelp') }}</div>
        </el-form-item>
        <el-form-item :label="t('ca.genericEAB')">
          <el-switch v-model="genericForm.eab_required" :disabled="genericForm.protocol === 'v1'" />
        </el-form-item>
        <template v-if="genericForm.eab_required && genericForm.protocol !== 'v1'">
          <el-form-item :label="t('ca.eabKid')" required>
            <el-input v-model="genericForm.eab_kid" :placeholder="t('ca.eabKidPlaceholder')" maxlength="200" />
          </el-form-item>
          <el-form-item :label="t('ca.eabHmac')" required>
            <el-input
              v-model="genericForm.eab_hmac"
              type="password"
              show-password
              :placeholder="genericEditing?.eab_hmac_set ? t('ca.eabHmacPlaceholderSet') : t('ca.eabHmacPlaceholderNew')"
            />
            <div class="form-help">{{ t('ca.eabHelp') }}</div>
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="genericDialogVisible = false">{{ t('ca.cancel') }}</el-button>
        <el-button type="primary" :loading="genericSaving" @click="saveGeneric">{{ t('ca.save') }}</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="dialogVisible" :title="t('ca.eabTitle', { ca: editing?.label || '' })" width="500px" destroy-on-close>
      <el-alert
        v-if="editing?.desc"
        :title="editing.desc"
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 18px"
      />
      <el-form :model="form" label-position="top">
        <el-form-item :label="t('ca.eabKid')" required>
          <el-input v-model="form.eab_kid" :placeholder="t('ca.eabKidPlaceholder')" maxlength="200" />
        </el-form-item>
        <el-form-item :label="t('ca.eabHmac')" required>
          <el-input
            v-model="form.eab_hmac"
            type="password"
            show-password
            :placeholder="editing?.eab_hmac_set ? t('ca.eabHmacPlaceholderSet') : t('ca.eabHmacPlaceholderNew')"
          />
          <div class="form-help">{{ t('ca.eabHelp') }}</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button
          v-if="editing?.eab_kid || editing?.eab_hmac_set"
          type="danger"
          plain
          :loading="saving"
          @click="clearEAB"
        >
          {{ t('ca.clear') }}
        </el-button>
        <el-button @click="dialogVisible = false">{{ t('ca.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="saveEAB">{{ t('ca.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, InfoFilled, Plus, Refresh, Search } from '@element-plus/icons-vue'
import { api } from '../api'
import { loadMeta, loadSettings } from '../store'
import type { ACMECA, CADef, CAConfig, CAPool, CAPoolItem, CAPoolStrategy, MessageResult, Meta, Settings } from '../types'

interface ACMEForm {
  acme_email: string
  acme_ca: string
  acme_ca_pool_id: number
  acme_key_type: string
  acme_renew_days: number
}

type UpstreamSource = 'builtin' | 'custom'
type UpstreamFilter = 'all' | UpstreamSource
type IssuanceMode = 'single' | 'pool'

interface CAUpstreamRow {
  value: string
  label: string
  desc: string
  url: string
  protocol: 'v1' | 'v2'
  source: UpstreamSource
  eabRequired: boolean
  eabConfigured: boolean
  isDefault: boolean
  id?: number
  builtin?: CAConfig
  custom?: ACMECA
}

const { t } = useI18n()
const tab = ref<'upstreams' | 'pools' | 'defaults'>('upstreams')
const items = ref<CAConfig[]>([])
const genericItems = ref<ACMECA[]>([])
const pools = ref<CAPool[]>([])
const meta = ref<Meta | null>(null)
const loading = ref(false)
const poolSaving = ref(false)
const poolDialogVisible = ref(false)
const poolEditingId = ref<number | null>(null)
const saving = ref(false)
const savingACME = ref(false)
const dialogVisible = ref(false)
const editing = ref<CAConfig | null>(null)

const upstreamFilter = ref<UpstreamFilter>('all')
const upstreamKeyword = ref('')

const genericDialogVisible = ref(false)
const genericSaving = ref(false)
const genericEditingId = ref<number | null>(null)
const genericEditing = ref<ACMECA | null>(null)

const form = reactive({ eab_kid: '', eab_hmac: '' })
const genericForm = reactive({
  name: '',
  directory_url: '',
  protocol: 'v2',
  eab_required: true,
  eab_kid: '',
  eab_hmac: ''
})

const poolForm = reactive<{
  name: string
  strategy: CAPoolStrategy
  enabled: boolean
  items: CAPoolItem[]
}>({
  name: '',
  strategy: 'round_robin',
  enabled: true,
  items: []
})

const acme = reactive<ACMEForm>({
  acme_email: '',
  acme_ca: 'letsencrypt',
  acme_ca_pool_id: 0,
  acme_key_type: 'ecdsa',
  acme_renew_days: 30
})

const mode = ref<IssuanceMode>('single')

const upstreamRows = computed<CAUpstreamRow[]>(() => {
  const rows: CAUpstreamRow[] = items.value.map((ca) => ({
    value: ca.value,
    label: ca.label,
    desc: ca.desc || '',
    url: ca.url || '',
    protocol: ca.protocol === 'v1' ? 'v1' : 'v2',
    source: 'builtin',
    eabRequired: !!ca.eab,
    eabConfigured: ca.eab ? ca.eab_hmac_set : false,
    isDefault: ca.is_default,
    builtin: ca
  }))
  for (const ca of genericItems.value) {
    const value = `acme-ca:${ca.id}`
    rows.push({
      value,
      label: ca.name,
      desc: t('ca.genericTitle'),
      url: ca.directory_url,
      protocol: ca.protocol === 'v1' ? 'v1' : 'v2',
      source: 'custom',
      eabRequired: ca.eab_required,
      eabConfigured: ca.eab_required ? ca.eab_hmac_set : false,
      isDefault: acme.acme_ca === value,
      id: ca.id,
      custom: ca
    })
  }
  return rows
})

const builtinCount = computed(() => upstreamRows.value.filter((row) => row.source === 'builtin').length)
const customCount = computed(() => upstreamRows.value.filter((row) => row.source === 'custom').length)

const filteredUpstreams = computed(() => {
  const keyword = upstreamKeyword.value.trim().toLowerCase()
  return upstreamRows.value.filter((row) => {
    if (upstreamFilter.value !== 'all' && row.source !== upstreamFilter.value) return false
    if (!keyword) return true
    return [row.label, row.url, row.desc].some((value) => value.toLowerCase().includes(keyword))
  })
})

const defaultPool = computed(() => pools.value.find((pool) => pool.id === acme.acme_ca_pool_id))
const defaultPoolActive = computed(
  () => !!defaultPool.value?.enabled && (defaultPool.value.items || []).some((item) => item.enabled)
)
const defaultCALabel = computed(() => caOptionLabel(acme.acme_ca))
const effectiveSummary = computed(() => {
  if (mode.value === 'single') {
    return t('ca.effectiveSingle', { ca: defaultCALabel.value })
  }
  if (!defaultPool.value || !defaultPoolActive.value) {
    return t('ca.poolUnavailable', { ca: defaultCALabel.value })
  }
  return t('ca.effectivePool', { pool: defaultPool.value.name, ca: defaultCALabel.value })
})

function applySettings(data: Settings): void {
  Object.assign(acme, {
    acme_email: data.acme_email || '',
    acme_ca: data.acme_ca || 'letsencrypt',
    acme_ca_pool_id: data.acme_ca_pool_id || 0,
    acme_key_type: data.acme_key_type || 'ecdsa',
    acme_renew_days: data.acme_renew_days || 30
  })
  mode.value = acme.acme_ca_pool_id > 0 ? 'pool' : 'single'
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [caItems, generic, poolItems, metaData, settingData] = await Promise.all([
      api.get<CAConfig[]>('/ca-configs'),
      api.get<ACMECA[]>('/acme-cas'),
      api.get<CAPool[]>('/ca-pools'),
      loadMeta(true),
      loadSettings(true)
    ])
    items.value = caItems || []
    genericItems.value = generic || []
    pools.value = poolItems || []
    meta.value = metaData
    applySettings(settingData)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

function strategyLabel(value?: string): string {
  switch (value) {
    case 'random':
      return t('ca.strategyRandom')
    case 'priority':
      return t('ca.strategyPriority')
    default:
      return t('ca.strategyRoundRobin')
  }
}

function caOptionLabel(value: string): string {
  return (meta.value?.cas || []).find((item) => item.value === value)?.label || value
}

function enabledCandidates(pool: CAPool): CAPoolItem[] {
  return (pool.items || []).filter((item) => item.enabled).sort((a, b) => a.priority - b.priority)
}

function poolChips(pool: CAPool): string[] {
  return enabledCandidates(pool)
    .slice(0, 3)
    .map((item) => caOptionLabel(item.ca_value))
}

function poolHiddenCount(pool: CAPool): number {
  return Math.max(0, enabledCandidates(pool).length - 3)
}

function poolTooltip(pool: CAPool): string {
  return enabledCandidates(pool)
    .map((item) => `${caOptionLabel(item.ca_value)} · P${item.priority} · W${item.weight}`)
    .join('；')
}

function newPoolItem(): CAPoolItem {
  return { ca_value: '', priority: 10, weight: 1, enabled: true }
}

function openPoolCreate(): void {
  poolEditingId.value = null
  Object.assign(poolForm, {
    name: '',
    strategy: 'round_robin',
    enabled: true,
    items: [newPoolItem()]
  })
  poolDialogVisible.value = true
}

function openPoolEdit(row: CAPool): void {
  poolEditingId.value = row.id
  Object.assign(poolForm, {
    name: row.name,
    strategy: row.strategy,
    enabled: row.enabled,
    items: (row.items || []).map((item) => ({ ...item }))
  })
  poolDialogVisible.value = true
}

function addPoolItem(): void {
  poolForm.items.push(newPoolItem())
}

function removePoolItem(index: number): void {
  poolForm.items.splice(index, 1)
}

// 候选下拉框只展示未被其他行占用的 CA，避免同一调度组内重复选择。
// 保留当前行自身的值，防止已选项被过滤后下拉框无法正常回显。
function poolCAOptions(index: number): CADef[] {
  const current = poolForm.items[index]?.ca_value || ''
  const taken = new Set(
    poolForm.items
      .filter((_, itemIndex) => itemIndex !== index)
      .map((item) => item.ca_value)
      .filter((value) => value !== '')
  )
  return (meta.value?.cas || []).filter((ca) => ca.value === current || !taken.has(ca.value))
}

function handlePoolCommand(command: string, row: CAPool): void {
  if (command === 'delete') void deletePool(row)
}

async function savePool(): Promise<void> {
  if (!poolForm.name.trim()) {
    ElMessage.warning(t('ca.poolNameRequired'))
    return
  }
  const items = poolForm.items
    .filter((item) => item.ca_value)
    .map((item, index) => ({
      ca_value: item.ca_value,
      priority: item.priority ?? index * 10,
      weight: item.weight || 1,
      enabled: item.enabled
    }))
  if (!items.some((item) => item.enabled)) {
    ElMessage.warning(t('ca.poolEmptyWarning'))
    return
  }
  poolSaving.value = true
  try {
    const payload = {
      name: poolForm.name.trim(),
      strategy: poolForm.strategy,
      enabled: poolForm.enabled,
      items
    }
    const saved = await (poolEditingId.value
      ? api.put(`/ca-pools/${poolEditingId.value}`, payload)
      : api.post('/ca-pools', payload))
    if (saved === null) return
    ElMessage.success(poolEditingId.value ? t('ca.poolUpdated') : t('ca.poolCreated'))
    poolDialogVisible.value = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    poolSaving.value = false
  }
}

async function deletePool(row: CAPool): Promise<void> {
  try {
    await ElMessageBox.confirm(t('ca.poolDeleteConfirm', { name: row.name }), t('ca.poolDeleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    const deleted = await api.delete(`/ca-pools/${row.id}`)
    if (deleted === null) return
    ElMessage.success(t('ca.poolDeleted'))
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function openGenericCreate(): void {
  genericEditingId.value = null
  genericEditing.value = null
  Object.assign(genericForm, {
    name: '',
    directory_url: '',
    protocol: 'v2',
    eab_required: true,
    eab_kid: '',
    eab_hmac: ''
  })
  genericDialogVisible.value = true
}

function openGenericEdit(row: ACMECA): void {
  genericEditingId.value = row.id
  genericEditing.value = row
  Object.assign(genericForm, {
    name: row.name,
    directory_url: row.directory_url,
    protocol: row.protocol || 'v2',
    eab_required: row.eab_required,
    eab_kid: row.eab_kid || '',
    eab_hmac: ''
  })
  genericDialogVisible.value = true
}

function openBuiltinEAB(row: CAUpstreamRow): void {
  if (row.builtin) openConfig(row.builtin)
}

function editCustomUpstream(row: CAUpstreamRow): void {
  if (row.custom) openGenericEdit(row.custom)
}

function handleUpstreamCommand(command: string, row: CAUpstreamRow): void {
  if (command === 'delete' && row.custom) void deleteGeneric(row.custom)
}

function onGenericProtocolChange(value: string): void {
  // ACME v2 默认启用 EAB；v1 不支持 EAB。
  genericForm.eab_required = value === 'v2'
}

async function saveGeneric(): Promise<void> {
  if (!genericForm.name.trim()) {
    ElMessage.warning(t('ca.genericNameRequired'))
    return
  }
  if (!genericForm.directory_url.trim()) {
    ElMessage.warning(t('ca.genericURLRequired'))
    return
  }
  if (genericForm.eab_required) {
    if (!genericForm.eab_kid.trim()) {
      ElMessage.warning(t('ca.kidRequired'))
      return
    }
    if (!genericForm.eab_hmac.trim() && !genericEditing.value?.eab_hmac_set) {
      ElMessage.warning(t('ca.hmacRequired'))
      return
    }
  }
  genericSaving.value = true
  try {
    const payload = {
      name: genericForm.name.trim(),
      directory_url: genericForm.directory_url.trim(),
      protocol: genericForm.protocol,
      eab_required: genericForm.protocol === 'v1' ? false : genericForm.eab_required,
      eab_kid: genericForm.eab_kid.trim(),
      eab_hmac: genericForm.eab_hmac.trim()
    }
    const saved = await (genericEditingId.value
      ? api.put(`/acme-cas/${genericEditingId.value}`, payload)
      : api.post('/acme-cas', payload))
    if (saved === null) return
    ElMessage.success(genericEditingId.value ? t('ca.genericUpdated') : t('ca.genericCreated'))
    genericDialogVisible.value = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    genericSaving.value = false
  }
}

async function deleteGeneric(row: ACMECA): Promise<void> {
  try {
    await ElMessageBox.confirm(t('ca.genericDeleteConfirm', { name: row.name }), t('ca.genericDeleteTitle'), {
      confirmButtonText: t('ca.genericDelete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    const deleted = await api.delete(`/acme-cas/${row.id}`)
    if (deleted === null) return
    ElMessage.success(t('ca.genericDeleted'))
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function saveACME(): Promise<void> {
  if (mode.value === 'pool') {
    if (!acme.acme_ca_pool_id) {
      ElMessage.warning(t('ca.defaultPoolRequired'))
      return
    }
    if (!defaultPoolActive.value) {
      ElMessage.warning(t('ca.defaultPoolUnavailable'))
      return
    }
  }
  savingACME.value = true
  try {
    const saved = await api.put('/settings', {
      acme_email: acme.acme_email,
      acme_ca: acme.acme_ca || 'letsencrypt',
      acme_ca_pool_id: mode.value === 'pool' ? acme.acme_ca_pool_id : 0,
      acme_key_type: acme.acme_key_type,
      acme_renew_days: acme.acme_renew_days
    })
    if (saved === null) return
    ElMessage.success(t('ca.saved'))
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingACME.value = false
  }
}

function openConfig(ca: CAConfig): void {
  editing.value = ca
  form.eab_kid = ca.eab_kid || ''
  form.eab_hmac = ''
  dialogVisible.value = true
}

async function saveEAB(): Promise<void> {
  if (!editing.value) return
  const currentCA = editing.value
  if (!form.eab_kid.trim()) {
    ElMessage.warning(t('ca.kidRequired'))
    return
  }
  if (!form.eab_hmac.trim() && !editing.value.eab_hmac_set) {
    ElMessage.warning(t('ca.hmacRequired'))
    return
  }
  saving.value = true
  try {
    const data = await api.put<MessageResult>(`/ca-configs/${currentCA.value}`, {
      eab_kid: form.eab_kid.trim(),
      eab_hmac: form.eab_hmac.trim()
    })
    if (data === null) return
    ElMessage.success(data?.message || t('ca.eabSaved'))
    dialogVisible.value = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function clearEAB(): Promise<void> {
  if (!editing.value) return
  const currentCA = editing.value
  try {
    await ElMessageBox.confirm(t('ca.clearConfirm', { ca: editing.value.label }), t('ca.clearTitle'), {
      confirmButtonText: t('ca.clear'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  saving.value = true
  try {
    const data = await api.put<MessageResult>(`/ca-configs/${currentCA.value}`, { clear: true })
    if (data === null) return
    ElMessage.success(data?.message || t('ca.eabCleared'))
    dialogVisible.value = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.ca-card {
  padding: 0;
  overflow: hidden;
}

.ca-tabs :deep(.el-tabs__header) {
  margin: 0;
  padding: 0 20px;
}

.ca-tabs :deep(.el-tabs__item) {
  padding: 0 12px;
}

.ca-tabs :deep(.el-tabs__item:first-child) {
  padding-left: 0;
}

.filter-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 12px 20px;
  flex: none;
}

.filter-row .muted {
  font-size: 12px;
}

.filter-spacer {
  margin-left: auto;
}

.ca-notice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 9px 20px;
  background: #f8fafc;
  border-bottom: 1px solid var(--kp-border);
  color: #64748b;
  font-size: 12px;
  line-height: 1.5;
  flex: none;
}

.ca-notice .el-icon {
  margin-top: 1px;
  color: #94a3b8;
}

.ca-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
  font-weight: 600;
  font-size: 14px;
  color: #0f172a;
}

.ca-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ca-desc {
  margin-top: 3px;
  color: #94a3b8;
  font-size: 12px;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ca-url {
  display: block;
  color: #475569;
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ca-card :deep(.el-table .cell) {
  padding: 0 20px;
}

.ca-chips {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.ca-empty {
  padding: 40px 0;
  color: #94a3b8;
}

.ca-form-scroll {
  padding: 4px 20px 20px;
}

.ca-effective {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 4px 0 18px;
  padding: 10px 12px;
  background: #f8fafc;
  border: 1px solid var(--kp-border);
  color: #475569;
  font-size: 12px;
}

.ca-effective.is-warning {
  background: #fffbeb;
  border-color: #fde68a;
  color: #92400e;
}

.ca-mode {
  margin-top: 6px;
  padding-top: 18px;
  border-top: 1px solid var(--kp-border);
}

.ca-mode-title {
  margin-bottom: 12px;
  font-weight: 600;
  font-size: 14px;
  color: #0f172a;
}

.ca-mode-fields {
  margin-top: 16px;
}

.ca-form-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 20px;
  border-top: 1px solid var(--kp-border);
  flex: none;
}

.ca-form-footer .muted {
  font-size: 12px;
}

.pool-item {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

/* 桌面端：标签页内部接上高度链，只有表体/表单滚动。 */
@media (min-width: 861px) {
  .ca-card {
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .ca-card :deep(.el-tabs) {
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .ca-card :deep(.el-tabs__content) {
    flex: 1 1 auto;
    min-height: 0;
    overflow: hidden;
  }

  .ca-card :deep(.el-tab-pane) {
    height: 100%;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .ca-card :deep(.el-tab-pane > .table-scroll) {
    flex: 1 1 auto;
    min-height: 0;
  }

  .ca-card :deep(.el-tab-pane > .table-scroll > .el-table) {
    min-width: 0;
  }

  .ca-form-scroll {
    flex: 1 1 auto;
    min-height: 0;
    overflow-y: auto;
  }

  .ca-form-footer {
    flex: none;
  }
}

@media (max-width: 860px) {
  .ca-tabs :deep(.el-tabs__header) {
    padding: 0 12px;
  }

  .filter-row {
    padding: 12px;
  }

  .filter-row :deep(.el-input) {
    width: 100% !important;
  }

  .filter-spacer {
    margin-left: 0;
  }

  .ca-notice {
    padding: 9px 12px;
  }

  .ca-form-scroll {
    padding: 4px 12px 16px;
  }

  .ca-form-footer {
    padding: 12px;
  }
}

@media (max-width: 620px) {
  .pool-item {
    flex-wrap: wrap;
  }

  .pool-item :deep(.el-select) {
    flex: 1 1 100%;
  }

  .ca-form-footer {
    flex-direction: column;
    align-items: stretch;
  }

  .ca-form-footer .el-button {
    width: 100%;
  }
}
</style>
