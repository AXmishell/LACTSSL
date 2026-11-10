<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('acmeServer.title') }}</h1>
        <p>{{ t('acmeServer.subtitle') }}</p>
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

    <div class="k-card" style="padding: 0; overflow: hidden">
      <el-table
        ref="tableRef"
        :data="endpoints"
        row-key="id"
        v-loading="loading"
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

    <div v-if="selectedEndpoints.length" class="batch-bar">
      <div class="batch-actions">
        <el-button size="small" type="danger" plain :icon="Delete" :loading="batchDeleting" @click="batchDelete">
          {{ t('acmeServer.batchDelete') }}
        </el-button>
        <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('acmeServer.clearSelection') }}</el-button>
      </div>
      <span class="batch-count">{{ t('acmeServer.selectedCount', { count: selectedEndpoints.length }) }}</span>
    </div>

    <!-- 端点配置 -->
    <el-dialog v-model="dialogVisible" :title="editing ? t('acmeServer.editTitle') : t('acmeServer.addTitle')" width="760px" destroy-on-close>
      <el-form :model="form" label-position="top">
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('acmeServer.name')" required>
            <el-input v-model="form.name" :placeholder="t('acmeServer.namePlaceholder')" maxlength="100" />
          </el-form-item>
          <el-form-item :label="t('acmeServer.slug')" required>
            <el-input v-model="form.slug" :placeholder="t('acmeServer.slugPlaceholder')" maxlength="64" />
            <div class="form-help">{{ t('acmeServer.slugHelp', { url: directoryPreview }) }}</div>
          </el-form-item>
        </div>
        <el-form-item :label="t('acmeServer.ca')" required>
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
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('acmeServer.enabled')">
            <el-switch v-model="form.enabled" />
            <div class="form-help">{{ t('acmeServer.enabledHelp') }}</div>
          </el-form-item>
          <el-form-item :label="t('acmeServer.eabRequired')">
            <el-switch v-model="form.eab_required" />
            <div class="form-help" :class="{ 'acme-danger': !form.eab_required }">
              {{ form.eab_required ? t('acmeServer.eabRequiredHelp') : t('acmeServer.eabOptionalHelp') }}
            </div>
          </el-form-item>
        </div>
        <el-form-item :label="t('acmeServer.allowedDomains')">
          <el-input
            v-model="form.allowed_domains"
            type="textarea"
            :rows="3"
            :placeholder="t('acmeServer.allowedDomainsPlaceholder')"
          />
          <div class="form-help">{{ t('acmeServer.allowedDomainsHelp') }}</div>
        </el-form-item>
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('acmeServer.challengeTypes')">
            <el-checkbox v-model="form.http01_enabled">HTTP-01</el-checkbox>
            <el-checkbox v-model="form.dns01_enabled">DNS-01</el-checkbox>
            <el-checkbox v-model="form.tls_alpn_01_enabled">TLS-ALPN-01</el-checkbox>
            <div class="form-help">{{ t('acmeServer.challengeHelp') }}</div>
          </el-form-item>
          <el-form-item :label="t('acmeServer.wildcard')">
            <el-switch v-model="form.allow_wildcard" />
            <div class="form-help">{{ t('acmeServer.wildcardHelp') }}</div>
          </el-form-item>
        </div>
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('acmeServer.allowIP')">
            <el-switch v-model="form.allow_ip" />
            <div class="form-help">{{ t('acmeServer.allowIPHelp') }}</div>
          </el-form-item>
          <el-form-item :label="t('acmeServer.allowedIPCIDRs')">
            <el-input
              v-model="form.allowed_ip_cidrs"
              type="textarea"
              :rows="2"
              :placeholder="t('acmeServer.allowedIPCIDRsPlaceholder')"
            />
            <div class="form-help">{{ t('acmeServer.allowedIPCIDRsHelp') }}</div>
          </el-form-item>
        </div>
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('acmeServer.allowPrivateHTTP01')">
            <el-switch v-model="form.allow_private_http01" />
            <div class="form-help">{{ t('acmeServer.allowPrivateHTTP01Help') }}</div>
          </el-form-item>
          <el-form-item :label="t('acmeServer.http01AllowedCIDRs')">
            <el-input
              v-model="form.http01_allowed_cidrs"
              type="textarea"
              :rows="2"
              :placeholder="t('acmeServer.http01AllowedCIDRsPlaceholder')"
            />
            <div class="form-help">{{ t('acmeServer.http01AllowedCIDRsHelp') }}</div>
          </el-form-item>
          <el-form-item :label="t('acmeServer.allowPrivateTLSALPN01')">
            <el-switch v-model="form.allow_private_tls_alpn_01" />
            <div class="form-help">{{ t('acmeServer.allowPrivateTLSALPN01Help') }}</div>
          </el-form-item>
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
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('acmeServer.validityDays')">
            <el-input-number v-model="form.validity_days" :min="1" :max="3650" style="width: 100%" />
          </el-form-item>
          <el-form-item :label="t('acmeServer.maxOrdersPerHour')">
            <el-input-number v-model="form.max_orders_per_hour" :min="1" :max="10000" style="width: 100%" />
          </el-form-item>
        </div>
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
import { Close, Connection, CopyDocument, DataAnalysis, Delete, Key, Plus, Refresh } from '@element-plus/icons-vue'
import { api, copyText } from '../api'
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
const batchDeleting = ref(false)

const dialogVisible = ref(false)
const editing = ref<ACMEEndpoint | null>(null)
const form = reactive<EndpointForm>(emptyForm())

const directoryPreview = computed(() => {
  const value = form.slug.trim()
  if (!value) return `${window.location.origin}/acme/v2/directory`
  if (/^https?:\/\//i.test(value)) return value
  if (value.startsWith('/')) return `${window.location.origin}${value}`
  return `${window.location.origin}/acme/${value}/directory`
})

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
    slug: '/acme/v2/directory',
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

// slugFromPath 从完整路径（如 /acme/v2/directory）中提取端点 slug。
function slugFromPath(value: string): string {
  const trimmed = value.trim()
  const match = trimmed.match(/\/acme\/([a-z0-9-]+)(?:\/directory)?\/?$/i)
  if (match) return match[1].toLowerCase()
  if (/^[a-z0-9-]+$/i.test(trimmed)) return trimmed.toLowerCase()
  return ''
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
    slug: `/acme/${row.slug}/directory`,
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

async function submitEndpoint(): Promise<void> {
  const slug = slugFromPath(form.slug)
  if (!form.name.trim() || !slug) {
    ElMessage.warning(!form.name.trim() ? t('acmeServer.nameRequired') : t('acmeServer.slugInvalid'))
    return
  }
  if (!form.ca_id) {
    ElMessage.warning(t('acmeServer.caRequired'))
    return
  }
  const domains = splitList(form.allowed_domains)
  const ipCIDRs = splitList(form.allowed_ip_cidrs)
  if (form.enabled && domains.length === 0 && !form.allow_ip) {
    ElMessage.warning(t('acmeServer.domainsRequired'))
    return
  }
  if (form.allow_ip && ipCIDRs.length === 0) {
    ElMessage.warning(t('acmeServer.ipCIDRsRequired'))
    return
  }
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
    if (editing.value) {
      await api.put(`/acme-endpoints/${editing.value.id}`, payload)
      ElMessage.success(t('acmeServer.updated'))
    } else {
      await api.post('/acme-endpoints', payload)
      ElMessage.success(t('acmeServer.created'))
    }
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
    await api.delete(`/acme-endpoints/${row.id}`)
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
  eabSaving.value = true
  try {
    createdEAB.value = await api.post<ACMEEABCreated>(`/acme-endpoints/${eabEndpoint.value.id}/eab`, {
      label: eabForm.label.trim(),
      expires_days: eabForm.expires_days
    })
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
    await api.delete(`/acme-eab/${row.id}`)
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
</style>
