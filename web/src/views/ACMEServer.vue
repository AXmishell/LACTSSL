<template>
  <div class="page table-page">
    <div class="page-head">
      <div>
        <h1>{{ t('acmeServer.title') }}</h1>
      </div>
      <div class="page-actions">
        <el-button :icon="DataAnalysis" @click="router.push('/acme-diagnostics')">{{ t('acmeServer.diagnostics') }}</el-button>
        <el-button type="primary" :icon="Plus" @click="openCreate">{{ t('acmeServer.add') }}</el-button>
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <el-alert
      :title="t('acmeServer.alert')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 18px"
    />

    <div class="k-card table-fill" style="padding: 0; overflow: hidden">
      <div class="table-scroll">
        <el-table
          ref="tableRef"
          :data="endpoints"
          row-key="id"
          v-loading="loading"
          height="100%"
          style="width: 100%"
          @selection-change="onSelectionChange"
        >
          <el-table-column type="selection" width="48" fixed="left" :reserve-selection="true" />
          <el-table-column min-width="220" :label="t('acmeServer.endpoint')">
            <template #default="{ row }">
              <div class="acme-name">{{ row.name }}</div>
              <div class="acme-sub mono">/acme/{{ row.slug }}/directory</div>
              <div class="acme-tags">
                <el-tag :type="row.enabled ? 'success' : 'info'" effect="light" round size="small">
                  {{ row.enabled ? t('common.enabled') : t('common.disabled') }}
                </el-tag>
                <el-tag :type="row.eab_required ? 'warning' : 'danger'" effect="light" round size="small">
                  {{ row.eab_required ? t('acmeServer.eabRequiredTag') : t('acmeServer.eabOptionalTag') }}
                </el-tag>
              </div>
            </template>
          </el-table-column>
          <el-table-column width="150" :label="t('acmeServer.ca')">
            <template #default="{ row }">
              <div>{{ row.ca_name || `#${row.ca_id}` }}</div>
              <div class="acme-sub">{{ t('acmeServer.validityDaysShort', { days: row.validity_days }) }}</div>
            </template>
          </el-table-column>
          <el-table-column min-width="300" :label="t('acmeServer.directoryURL')">
            <template #default="{ row }">
              <div class="acme-url">
                <code class="mono break-all">{{ displayDirectoryURL(row.slug) }}</code>
                <el-button text type="primary" size="small" :icon="CopyDocument" @click="copy(displayDirectoryURL(row.slug))" />
              </div>
            </template>
          </el-table-column>
          <el-table-column width="190" :label="t('acmeServer.challenges')">
            <template #default="{ row }">
              <div class="acme-tags">
                <el-tag v-if="row.http01_enabled" effect="light" round size="small">HTTP-01</el-tag>
                <el-tag v-if="row.dns01_enabled" effect="light" round size="small">DNS-01</el-tag>
                <el-tag v-if="row.tls_alpn_01_enabled" effect="light" round size="small">TLS-ALPN-01</el-tag>
                <el-tag v-if="row.allow_wildcard" type="warning" effect="light" round size="small">*</el-tag>
                <el-tag v-if="row.allow_ip" type="info" effect="light" round size="small">IP</el-tag>
              </div>
              <div class="acme-sub">{{ t('acmeServer.domainsCount', { count: row.allowed_domains?.length || 0 }) }}</div>
            </template>
          </el-table-column>
          <el-table-column width="250" :label="t('common.actions')" fixed="right">
            <template #default="{ row }">
              <el-button text type="primary" size="small" @click="openEdit(row)">{{ t('common.edit') }}</el-button>
              <el-button text size="small" :icon="Key" @click="openEAB(row)">{{ t('acmeServer.eab') }}</el-button>
              <el-button text type="danger" size="small" @click="removeEndpoint(row)">{{ t('common.delete') }}</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div style="padding: 40px 0; color: #94a3b8">
              <el-icon size="30"><Connection /></el-icon>
              <p>{{ t('acmeServer.empty') }}</p>
            </div>
          </template>
        </el-table>
      </div>
      <div class="batch-foot" :class="{ 'is-selecting': showBatchBar && selectedEndpoints.length }">
        <div v-if="showBatchBar && selectedEndpoints.length" class="batch-bar">
          <div class="batch-actions">
            <el-button size="small" type="danger" plain :icon="Delete" :loading="batchDeleting" @click="batchDelete">
              {{ t('acmeServer.batchDelete') }}
            </el-button>
            <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('acmeServer.clearSelection') }}</el-button>
          </div>
          <span class="batch-count">{{ t('acmeServer.selectedCount', { count: selectedEndpoints.length }) }}</span>
        </div>
        <span v-else class="muted">{{ t('common.totalItems', { count: endpoints.length }) }}</span>
      </div>
    </div>

    <!-- 端点配置 -->
    <el-dialog
      v-model="dialogVisible"
      :title="editing ? t('acmeServer.editTitle') : t('acmeServer.addTitle')"
      class="acme-endpoint-dialog"
      width="min(840px, 94vw)"
      top="5vh"
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent>
        <section class="form-section">
          <h3 class="form-section-title">{{ t('acmeServer.sectionBasic') }}</h3>
          <div class="grid grid-2" style="gap: 0 18px">
            <el-form-item :label="t('acmeServer.name')" prop="name">
              <el-input v-model="form.name" :placeholder="t('acmeServer.namePlaceholder')" maxlength="100" />
            </el-form-item>
            <el-form-item :label="t('acmeServer.slug')">
              <el-input :model-value="directoryPath" readonly class="path-fixed" />
              <div class="form-help">{{ t('acmeServer.slugFixedHelp', { url: directoryPreview }) }}</div>
            </el-form-item>
          </div>
          <div class="grid grid-2" style="gap: 0 18px">
            <el-form-item :label="t('acmeServer.ca')" prop="ca_id">
              <el-select v-model="form.ca_id" style="width: 100%" :placeholder="t('acmeServer.caPlaceholder')">
                <el-option
                  v-for="ca in privateCAs"
                  :key="ca.id"
                  :value="ca.id"
                  :label="`${ca.name} (${ca.common_name})`"
                />
              </el-select>
              <div class="form-help">{{ t('acmeServer.caHelp') }}</div>
            </el-form-item>
            <el-form-item :label="t('acmeServer.enabled')">
              <el-switch v-model="form.enabled" />
              <div class="form-help">{{ t('acmeServer.enabledHelp') }}</div>
            </el-form-item>
          </div>
        </section>

        <section class="form-section">
          <h3 class="form-section-title">{{ t('acmeServer.sectionAccess') }}</h3>
          <div class="grid grid-3" style="gap: 0 18px">
            <el-form-item :label="t('acmeServer.eabRequired')">
              <el-switch v-model="form.eab_required" />
              <div class="form-help" :class="{ 'acme-danger': !form.eab_required }">
                {{ form.eab_required ? t('acmeServer.eabRequiredHelp') : t('acmeServer.eabOptionalHelp') }}
              </div>
            </el-form-item>
            <el-form-item :label="t('acmeServer.wildcard')">
              <el-switch v-model="form.allow_wildcard" @change="onWildcardChange" />
              <div class="form-help">{{ t('acmeServer.wildcardHelp') }}</div>
            </el-form-item>
            <el-form-item :label="t('acmeServer.allowIP')">
              <el-switch v-model="form.allow_ip" />
              <div class="form-help">{{ t('acmeServer.allowIPHelp') }}</div>
            </el-form-item>
          </div>
          <el-form-item :label="t('acmeServer.allowedDomains')" prop="allowed_domains">
            <el-input
              v-model="form.allowed_domains"
              type="textarea"
              :rows="3"
              :placeholder="t('acmeServer.allowedDomainsPlaceholder')"
            />
            <div class="form-help">{{ t('acmeServer.allowedDomainsHelp') }}</div>
          </el-form-item>
          <el-form-item v-if="form.allow_ip" :label="t('acmeServer.allowedIPCIDRs')" prop="allowed_ip_cidrs">
            <el-input
              v-model="form.allowed_ip_cidrs"
              type="textarea"
              :rows="2"
              :placeholder="t('acmeServer.allowedIPCIDRsPlaceholder')"
            />
            <div class="form-help">{{ t('acmeServer.allowedIPCIDRsHelp') }}</div>
          </el-form-item>
        </section>

        <section class="form-section">
          <h3 class="form-section-title">{{ t('acmeServer.sectionChallenges') }}</h3>
          <el-form-item :label="t('acmeServer.challengeTypes')">
            <div class="challenge-row">
              <el-checkbox v-model="form.http01_enabled">HTTP-01</el-checkbox>
              <el-checkbox v-model="form.dns01_enabled" @change="onDNS01Change">DNS-01</el-checkbox>
              <el-checkbox v-model="form.tls_alpn_01_enabled">TLS-ALPN-01</el-checkbox>
            </div>
            <div class="form-help">{{ t('acmeServer.challengeHelp') }}</div>
          </el-form-item>

          <div v-if="form.http01_enabled" class="challenge-panel">
            <div class="challenge-panel-head">
              <span class="challenge-panel-title">HTTP-01</span>
              <div class="challenge-switch">
                <el-switch v-model="form.allow_private_http01" />
                <span>{{ t('acmeServer.allowPrivateAccess') }}</span>
              </div>
            </div>
            <div class="form-help challenge-panel-help">{{ t('acmeServer.allowPrivateHTTP01Help') }}</div>
            <el-form-item :label="t('acmeServer.http01AllowedCIDRs')">
              <el-input
                v-model="form.http01_allowed_cidrs"
                type="textarea"
                :rows="2"
                :placeholder="t('acmeServer.http01AllowedCIDRsPlaceholder')"
              />
              <div class="form-help">{{ t('acmeServer.http01AllowedCIDRsHelp') }}</div>
            </el-form-item>
          </div>

          <div v-if="form.tls_alpn_01_enabled" class="challenge-panel">
            <div class="challenge-panel-head">
              <span class="challenge-panel-title">TLS-ALPN-01</span>
              <div class="challenge-switch">
                <el-switch v-model="form.allow_private_tls_alpn_01" />
                <span>{{ t('acmeServer.allowPrivateAccess') }}</span>
              </div>
            </div>
            <div class="form-help challenge-panel-help">{{ t('acmeServer.allowPrivateTLSALPN01Help') }}</div>
            <el-form-item :label="t('acmeServer.tlsALPN01AllowedCIDRs')">
              <el-input
                v-model="form.tls_alpn_01_allowed_cidrs"
                type="textarea"
                :rows="2"
                :placeholder="t('acmeServer.tlsALPN01AllowedCIDRsPlaceholder')"
              />
              <div class="form-help">{{ t('acmeServer.tlsALPN01AllowedCIDRsHelp') }}</div>
            </el-form-item>
          </div>
        </section>

        <section class="form-section">
          <h3 class="form-section-title">{{ t('acmeServer.sectionIssuance') }}</h3>
          <div class="grid grid-2" style="gap: 0 18px">
            <el-form-item :label="t('acmeServer.validityDays')">
              <el-input-number v-model="form.validity_days" :min="1" :max="3650" style="width: 100%" />
            </el-form-item>
            <el-form-item :label="t('acmeServer.maxOrdersPerHour')">
              <el-input-number v-model="form.max_orders_per_hour" :min="1" :max="10000" style="width: 100%" />
            </el-form-item>
          </div>
        </section>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="submitEndpoint">{{ t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- EAB 凭据 -->
    <el-dialog v-model="eabVisible" :title="t('acmeServer.eabTitle', { name: eabEndpoint?.name || '' })" width="760px" destroy-on-close>
      <el-alert :title="t('acmeServer.eabHelp')" type="info" :closable="false" show-icon style="margin-bottom: 16px" />

      <el-alert v-if="createdEAB" type="success" :closable="false" show-icon style="margin-bottom: 16px">
        <template #title>{{ t('acmeServer.eabCreated') }}</template>
        <div class="acme-secret">
          <div><strong>kid</strong><code class="mono break-all">{{ createdEAB.kid }}</code><el-button text type="primary" size="small" :icon="CopyDocument" @click="copy(createdEAB.kid)" /></div>
          <div><strong>HMAC</strong><code class="mono break-all">{{ createdEAB.hmac_key }}</code><el-button text type="primary" size="small" :icon="CopyDocument" @click="copy(createdEAB.hmac_key)" /></div>
          <div class="form-help">{{ t('acmeServer.eabSecretHint') }}</div>
        </div>
      </el-alert>

      <el-form label-position="top">
        <div class="grid grid-2" style="gap: 0 18px; align-items: end">
          <el-form-item :label="t('acmeServer.eabLabel')">
            <el-input v-model="eabForm.label" :placeholder="t('acmeServer.eabLabelPlaceholder')" maxlength="100" />
          </el-form-item>
          <el-form-item :label="t('acmeServer.eabExpiresDays')">
            <div class="flex gap-10">
              <el-input-number v-model="eabForm.expires_days" :min="0" :max="3650" style="flex: 1" />
              <el-button type="primary" :loading="eabSaving" @click="createEAB">{{ t('acmeServer.eabCreate') }}</el-button>
            </div>
          </el-form-item>
        </div>
      </el-form>

      <div class="table-scroll">
        <el-table :data="eabItems" v-loading="eabLoading" style="width: 100%">
          <el-table-column min-width="190" :label="t('acmeServer.eabKid')">
            <template #default="{ row }">
              <code class="mono break-all">{{ row.kid }}</code>
            </template>
          </el-table-column>
          <el-table-column width="120" :label="t('acmeServer.eabLabel')">
            <template #default="{ row }">{{ row.label || t('common.none') }}</template>
          </el-table-column>
          <el-table-column width="110" :label="t('common.status')">
            <template #default="{ row }">
              <el-tag :type="row.used_at ? 'info' : 'success'" effect="light" round size="small">
                {{ row.used_at ? t('acmeServer.eabUsed') : t('acmeServer.eabUnused') }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column width="170" :label="t('acmeServer.eabExpiresAt')">
            <template #default="{ row }">{{ row.expires_at ? formatDate(row.expires_at) : t('acmeServer.never') }}</template>
          </el-table-column>
          <el-table-column width="80" :label="t('common.actions')" fixed="right">
            <template #default="{ row }">
              <el-button text type="danger" size="small" @click="removeEAB(row)">{{ t('common.delete') }}</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div style="padding: 28px 0; color: #94a3b8">{{ t('acmeServer.eabEmpty') }}</div>
          </template>
        </el-table>
      </div>
      <template #footer>
        <el-button @click="eabVisible = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Close, Connection, CopyDocument, DataAnalysis, Delete, Key, Plus, Refresh } from '@element-plus/icons-vue'
import { api, copyText } from '../api'
import { useDelayedBatchBar } from '../batchBar'
import { formatDate } from '../utils'
import type { ACMEEABCreated, ACMEEABCredential, ACMEEndpoint, PrivateCA } from '../types'

interface EndpointForm {
  name: string
  slug: string
  ca_id: number
  enabled: boolean
  eab_required: boolean
  allowed_domains: string
  allow_wildcard: boolean
  allow_ip: boolean
  allowed_ip_cidrs: string
  http01_enabled: boolean
  dns01_enabled: boolean
  tls_alpn_01_enabled: boolean
  allow_private_http01: boolean
  allow_private_tls_alpn_01: boolean
  http01_allowed_cidrs: string
  tls_alpn_01_allowed_cidrs: string
  validity_days: number
  max_orders_per_hour: number
}

const { t } = useI18n()
const router = useRouter()
const loading = ref(false)
const saving = ref(false)
const endpoints = ref<ACMEEndpoint[]>([])
const privateCAs = ref<PrivateCA[]>([])
const tableRef = ref<{ clearSelection: () => void } | null>(null)
const selectedEndpoints = ref<ACMEEndpoint[]>([])
const showBatchBar = useDelayedBatchBar(() => selectedEndpoints.value.length > 0)
const batchDeleting = ref(false)

// 新端点固定使用 v2 路径；编辑时保留原端点路径，避免改变客户端已配置的地址。
const FIXED_SLUG = 'v2'

const dialogVisible = ref(false)
const editing = ref<ACMEEndpoint | null>(null)
const formRef = ref<FormInstance>()
const form = reactive<EndpointForm>(emptyForm())

const rules = computed<FormRules>(() => ({
  name: [
    {
      validator: (_rule, value, callback) => {
        if (!String(value || '').trim()) callback(new Error(t('acmeServer.nameRequired')))
        else callback()
      },
      trigger: 'blur'
    }
  ],
  ca_id: [
    {
      validator: (_rule, value, callback) => {
        if (!Number(value)) callback(new Error(t('acmeServer.caRequired')))
        else callback()
      },
      trigger: 'change'
    }
  ],
  allowed_domains: [
    {
      validator: (_rule, value, callback) => {
        if (form.enabled && !form.allow_ip && splitList(String(value || '')).length === 0) {
          callback(new Error(t('acmeServer.domainsRequired')))
        } else callback()
      },
      trigger: 'blur'
    }
  ],
  allowed_ip_cidrs: [
    {
      validator: (_rule, value, callback) => {
        if (form.allow_ip && splitList(String(value || '')).length === 0) {
          callback(new Error(t('acmeServer.ipCIDRsRequired')))
        } else callback()
      },
      trigger: 'blur'
    }
  ]
}))

const directoryPath = computed(() => `/acme/${form.slug}/directory`)
const directoryPreview = computed(() => `${window.location.origin}${directoryPath.value}`)

// displayDirectoryURL 统一使用当前浏览器地址拼接端点地址，
// 避免开发代理与后端推导出的 Host 不一致。
function displayDirectoryURL(slug: string): string {
  return `${window.location.origin}/acme/${slug}/directory`
}

const eabVisible = ref(false)
const eabEndpoint = ref<ACMEEndpoint | null>(null)
const eabItems = ref<ACMEEABCredential[]>([])
const eabLoading = ref(false)
const eabSaving = ref(false)
const createdEAB = ref<ACMEEABCreated | null>(null)
const eabForm = reactive({ label: '', expires_days: 0 })

function emptyForm(): EndpointForm {
  return {
    name: '',
    slug: FIXED_SLUG,
    ca_id: 0,
    enabled: false,
    eab_required: true,
    allowed_domains: '',
    allow_wildcard: false,
    allow_ip: false,
    allowed_ip_cidrs: '',
    http01_enabled: true,
    dns01_enabled: true,
    tls_alpn_01_enabled: false,
    allow_private_http01: false,
    allow_private_tls_alpn_01: false,
    http01_allowed_cidrs: '',
    tls_alpn_01_allowed_cidrs: '',
    validity_days: 90,
    max_orders_per_hour: 60
  }
}

function splitList(value: string): string[] {
  return value
    .split(/[\n,;]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [endpointList, caList] = await Promise.all([
      api.get<ACMEEndpoint[]>('/acme-endpoints'),
      api.get<PrivateCA[]>('/private-cas')
    ])
    endpoints.value = endpointList || []
    privateCAs.value = caList || []
    clearSelection()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editing.value = null
  Object.assign(form, emptyForm())
  if (privateCAs.value.length > 0) form.ca_id = privateCAs.value[0].id
  dialogVisible.value = true
}

function openEdit(row: ACMEEndpoint): void {
  editing.value = row
  Object.assign(form, {
    name: row.name,
    slug: row.slug,
    ca_id: row.ca_id,
    enabled: row.enabled,
    eab_required: row.eab_required,
    allowed_domains: (row.allowed_domains || []).join('\n'),
    allow_wildcard: row.allow_wildcard,
    allow_ip: row.allow_ip,
    allowed_ip_cidrs: (row.allowed_ip_cidrs || []).join('\n'),
    http01_enabled: row.http01_enabled,
    dns01_enabled: row.dns01_enabled,
    tls_alpn_01_enabled: row.tls_alpn_01_enabled,
    allow_private_http01: row.allow_private_http01,
    allow_private_tls_alpn_01: row.allow_private_tls_alpn_01,
    http01_allowed_cidrs: (row.http01_allowed_cidrs || []).join('\n'),
    tls_alpn_01_allowed_cidrs: (row.tls_alpn_01_allowed_cidrs || []).join('\n'),
    validity_days: row.validity_days || 90,
    max_orders_per_hour: row.max_orders_per_hour || 60
  })
  dialogVisible.value = true
}

// 通配符证书只能使用 DNS-01，开启通配符时自动勾选 DNS-01。
function onWildcardChange(value: boolean): void {
  if (value) form.dns01_enabled = true
}

// 取消 DNS-01 时同步关闭通配符，避免保存无效组合。
function onDNS01Change(value: boolean): void {
  if (!value) form.allow_wildcard = false
}

async function submitEndpoint(): Promise<void> {
  const formEl = formRef.value
  if (!formEl) return
  try {
    await formEl.validate()
  } catch (fields) {
    const first = fields && typeof fields === 'object' ? Object.keys(fields as Record<string, unknown>)[0] : ''
    if (first) formEl.scrollToField(first)
    return
  }
  const slug = form.slug
  const domains = splitList(form.allowed_domains)
  const ipCIDRs = splitList(form.allowed_ip_cidrs)
  saving.value = true
  try {
    const payload = {
      name: form.name.trim(),
      slug,
      ca_id: form.ca_id,
      enabled: form.enabled,
      eab_required: form.eab_required,
      allowed_domains: domains,
      allow_wildcard: form.allow_wildcard,
      allow_ip: form.allow_ip,
      allowed_ip_cidrs: ipCIDRs,
      http01_enabled: form.http01_enabled,
      dns01_enabled: form.dns01_enabled,
      tls_alpn_01_enabled: form.tls_alpn_01_enabled,
      allow_private_http01: form.allow_private_http01,
      allow_private_tls_alpn_01: form.allow_private_tls_alpn_01,
      http01_allowed_cidrs: splitList(form.http01_allowed_cidrs),
      tls_alpn_01_allowed_cidrs: splitList(form.tls_alpn_01_allowed_cidrs),
      validity_days: form.validity_days,
      max_orders_per_hour: form.max_orders_per_hour
    }
    const saved = editing.value
        ? await api.put(`/acme-endpoints/${editing.value!.id}`, payload)
        : await api.post('/acme-endpoints', payload)
    if (saved === null) return
    ElMessage.success(t(editing.value ? 'acmeServer.updated' : 'acmeServer.created'))
    dialogVisible.value = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function removeEndpoint(row: ACMEEndpoint): Promise<void> {
  try {
    await ElMessageBox.confirm(
      t('acmeServer.deleteConfirm', { name: row.name }),
      t('acmeServer.deleteTitle'),
      { confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel'), type: 'warning' }
    )
  } catch (_) {
    return
  }
  try {
    const deleted = await api.delete(`/acme-endpoints/${row.id}`)
    if (deleted === null) return
    ElMessage.success(t('common.deleted'))
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function openEAB(row: ACMEEndpoint): Promise<void> {
  eabEndpoint.value = row
  createdEAB.value = null
  eabForm.label = ''
  eabForm.expires_days = 0
  eabVisible.value = true
  await loadEAB()
}

async function loadEAB(): Promise<void> {
  if (!eabEndpoint.value) return
  eabLoading.value = true
  try {
    eabItems.value = (await api.get<ACMEEABCredential[]>(`/acme-endpoints/${eabEndpoint.value.id}/eab`)) || []
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    eabLoading.value = false
  }
}

async function createEAB(): Promise<void> {
  if (!eabEndpoint.value) return
  const endpoint = eabEndpoint.value
  eabSaving.value = true
  try {
    createdEAB.value = await api.post<ACMEEABCreated>(`/acme-endpoints/${endpoint.id}/eab`, {
        label: eabForm.label.trim(),
        expires_days: eabForm.expires_days
      })
    if (createdEAB.value === null) return
    ElMessage.success(t('acmeServer.eabCreated'))
    await loadEAB()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    eabSaving.value = false
  }
}

async function removeEAB(row: ACMEEABCredential): Promise<void> {
  try {
    await ElMessageBox.confirm(t('acmeServer.eabDeleteConfirm', { kid: row.kid }), t('acmeServer.deleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    const deleted = await api.delete(`/acme-eab/${row.id}`)
    if (deleted === null) return
    ElMessage.success(t('common.deleted'))
    await loadEAB()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function copy(value: string): Promise<void> {
  if (await copyText(value)) {
    ElMessage.success(t('common.copied'))
  } else {
    ElMessage.warning(t('common.copyFailedManually'))
  }
}

function onSelectionChange(rows: ACMEEndpoint[]): void {
  selectedEndpoints.value = rows
}

function clearSelection(): void {
  tableRef.value?.clearSelection()
  selectedEndpoints.value = []
}

async function batchDelete(): Promise<void> {
  const list = [...selectedEndpoints.value]
  if (!list.length) return
  try {
    await ElMessageBox.confirm(t('acmeServer.batchDeleteConfirm', { count: list.length }), t('acmeServer.batchDeleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  batchDeleting.value = true
  let failed = 0
  try {
    for (const endpoint of list) {
      try {
        await api.delete(`/acme-endpoints/${endpoint.id}`)
      } catch (error: any) {
        failed++
        ElMessage.error(`${endpoint.name}: ${error.message}`)
      }
    }
  } finally {
    batchDeleting.value = false
  }
  if (failed) ElMessage.warning(t('acmeServer.batchDeleteFailed', { count: failed }))
  else ElMessage.success(t('acmeServer.batchDeleted', { count: list.length }))
  await load()
}

onMounted(load)
</script>

<style scoped>
/* 内容区独立滚动，标题和底部按钮始终可见。 */
:global(.acme-endpoint-dialog) {
  display: flex;
  flex-direction: column;
  max-height: 90vh;
  margin-bottom: 0;
}
:global(.acme-endpoint-dialog .el-dialog__header) {
  flex: none;
}
:global(.acme-endpoint-dialog .el-dialog__body) {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding-right: 4px;
  scrollbar-gutter: stable;
}
:global(.acme-endpoint-dialog .el-dialog__footer) {
  flex: none;
  padding-top: 12px;
  border-top: 1px solid var(--kp-border);
}
/* 按任务分区，用分隔线代替包围式边框，避免视觉噪音。 */
.form-section + .form-section {
  margin-top: 20px;
  padding-top: 18px;
  border-top: 1px solid var(--kp-border);
}
.form-section-title {
  margin: 0 0 14px;
  color: var(--kp-text);
  font-size: 14px;
  font-weight: 600;
}
.form-help {
  margin-top: 4px;
  color: #94a3b8;
  font-size: 12px;
  line-height: 1.5;
}
.path-fixed :deep(.el-input__wrapper) {
  background: #f8fafc;
}
.path-fixed :deep(.el-input__inner) {
  color: var(--kp-muted);
}
.challenge-row {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 20px;
}
.challenge-row :deep(.el-checkbox) {
  margin-right: 0;
}
/* 每个启用的验证方式对应一个独立配置块，关闭时整块隐藏。 */
.challenge-panel {
  margin-bottom: 16px;
  padding: 12px 14px 0;
  border: 1px solid var(--kp-border);
}
.challenge-panel:last-child {
  margin-bottom: 0;
}
.challenge-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  flex-wrap: wrap;
}
.challenge-panel-title {
  font-size: 13px;
  font-weight: 600;
}
.challenge-switch {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--kp-muted);
  font-size: 12px;
}
.challenge-panel-help {
  margin: 4px 0 12px;
}
.challenge-panel :deep(.el-form-item) {
  margin-bottom: 14px;
}
@media (max-width: 640px) {
  .challenge-panel-head {
    flex-direction: column;
    align-items: flex-start;
  }
}
.acme-name {
  font-weight: 600;
}
.acme-sub {
  color: #94a3b8;
  font-size: 12px;
  margin-top: 2px;
}
.acme-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}
.acme-url {
  display: flex;
  align-items: flex-start;
  gap: 4px;
}
.acme-url code {
  flex: 1;
  font-size: 12px;
  color: #475569;
}
.acme-danger {
  color: #dc2626;
}
.acme-secret {
  margin-top: 8px;
  display: grid;
  gap: 8px;
  font-size: 12px;
}
</style>
