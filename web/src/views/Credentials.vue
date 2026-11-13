<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ isDNS ? t('credentials.dnsTitle') : t('credentials.deployTitle') }}</h1>
        <p>{{ isDNS ? t('credentials.dnsSubtitle') : t('credentials.deploySubtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle @click="load" />
        <el-button type="primary" :icon="Plus" @click="openCreate">{{ isDNS ? t('credentials.addDns') : t('credentials.addDeploy') }}</el-button>
      </div>
    </div>

    <div v-if="!items.length && !loading" class="k-card" style="text-align: center; padding: 56px 20px">
      <el-icon size="34" color="#94a3b8"><component :is="isDNS ? DnsAccountIcon : 'UploadFilled'" /></el-icon>
      <h3 style="margin: 14px 0 6px">{{ isDNS ? t('credentials.emptyDnsTitle') : t('credentials.emptyDeployTitle') }}</h3>
      <p class="muted" style="margin-top: 0">{{ isDNS ? t('credentials.emptyDnsDesc') : t('credentials.emptyDeployDesc') }}</p>
      <el-button type="primary" style="margin-top: 12px" @click="openCreate">{{ t('credentials.addNow') }}</el-button>
    </div>

    <div v-else class="k-card cred-list" v-loading="loading">
      <div class="cred-list-head">
        <span class="cred-head-name">
          <el-checkbox
            :model-value="allSelected"
            :indeterminate="someSelected"
            :disabled="!items.length"
            @change="toggleAll"
          />
          <span>{{ t('common.name') }}</span>
        </span>
        <span>{{ t('common.remark') }}</span>
        <span>{{ t('credentials.time') }}</span>
        <span class="cred-head-actions">{{ t('common.actions') }}</span>
      </div>
      <div v-for="item in items" :key="item.id" class="cred-row" :class="{ 'is-selected': isSelected(item) }">
        <div class="cred-cell cred-name">
          <el-checkbox
            class="cred-check"
            :model-value="isSelected(item)"
            @change="(value: string | number | boolean) => toggleItem(item, Boolean(value))"
          />
          <div class="cred-icon" :class="{ 'has-logo': logoFor(item) }">
            <img v-if="logoFor(item)" :src="logoFor(item)" :alt="typeLabel(item)" loading="lazy" />
            <template v-else>{{ initials(item.name) }}</template>
          </div>
          <div class="cred-name-text">
            <div class="cred-title">{{ item.name }}</div>
            <div class="cred-meta" style="margin-top: 4px">
              <span class="tag-soft tag-blue">{{ typeLabel(item) }}</span>
            </div>
          </div>
        </div>
        <div class="cred-cell cred-remark cred-meta">{{ item.remark || t('credentials.noRemark') }}</div>
        <div class="cred-cell cred-time cred-meta">{{ t('credentials.updatedAt', { value: fromNow(item.updated_at) }) }}</div>
        <div class="cred-cell cred-actions">
          <el-button text type="primary" size="small" @click="openEdit(item)">{{ t('credentials.configure') }}</el-button>
          <el-dropdown trigger="click" @command="(cmd: string) => handleCommand(cmd, item)">
            <el-button text circle><el-icon><MoreFilled /></el-icon></el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="edit">{{ t('credentials.edit') }}</el-dropdown-item>
                <el-dropdown-item command="delete" divided>{{ t('common.delete') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
    </div>

    <div v-if="selectedIds.length" class="batch-bar">
      <div class="batch-actions">
        <el-button size="small" type="danger" plain :icon="Delete" :loading="batchDeleting" @click="batchDelete">
          {{ t('credentials.batchDelete') }}
        </el-button>
        <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('credentials.clearSelection') }}</el-button>
      </div>
      <span class="batch-count">{{ t('credentials.selectedCount', { count: selectedIds.length }) }}</span>
    </div>

    <div v-if="!isDNS" class="k-card" style="margin-top: 20px">
      <div class="card-head">
        <div>
          <h3>{{ t('credentials.recentDeployments') }}</h3>
          <div class="sub">{{ t('credentials.recentDeploymentsSub') }}</div>
        </div>
        <el-button text type="primary" @click="loadRecords">{{ t('credentials.refresh') }}</el-button>
      </div>
      <el-table :data="records" size="small" style="width: 100%">
        <el-table-column width="160" :label="t('credentials.time')">
          <template #default="{ row }"><span class="muted">{{ formatDate(row.created_at) }}</span></template>
        </el-table-column>
        <el-table-column min-width="170" :label="t('credentials.certificate')">
          <template #default="{ row }">
            <span class="mono" style="font-size: 12px">{{ certDomain(row.cert_id) }}</span>
          </template>
        </el-table-column>
        <el-table-column width="150" :label="t('credentials.deployTarget')">
          <template #default="{ row }">{{ row.account_name || t('credentials.unknownTarget') }}</template>
        </el-table-column>
        <el-table-column width="100" :label="t('credentials.status')">
          <template #default="{ row }">
            <el-tag :type="row.status === 'success' ? 'success' : 'danger'" size="small" effect="light" round>
              {{ row.status === 'success' ? t('credentials.success') : t('credentials.failed') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column min-width="260" :label="t('credentials.result')">
          <template #default="{ row }"><span class="muted" style="font-size: 12px">{{ row.message }}</span></template>
        </el-table-column>
        <template #empty>
          <div style="padding: 26px 0; color: #94a3b8">{{ t('credentials.noRecords') }}</div>
        </template>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" :title="`${editingId ? t('credentials.edit') : t('credentials.add')} · ${isDNS ? t('credentials.dnsTitle') : t('credentials.deployTitle')}`" width="600px" destroy-on-close>
      <el-form :model="form" label-position="top">
        <el-form-item :label="isDNS ? t('credentials.accountName') : t('credentials.deployName')" required>
          <el-input v-model="form.name" :placeholder="isDNS ? t('credentials.namePlaceholderDns') : t('credentials.namePlaceholderDeploy')" maxlength="60" />
        </el-form-item>
        <el-form-item :label="isDNS ? t('credentials.dnsProvider') : t('credentials.deployType')" required>
          <el-select v-model="typeValue" style="width: 100%" @change="onTypeChange">
            <el-option v-for="option in typeOptions" :key="option.value" :label="option.label" :value="option.value">
              <div class="flex-between" style="gap: 20px">
                <span>{{ option.label }}</span>
                <span class="muted" style="font-size: 11px">{{ option.desc }}</span>
              </div>
            </el-option>
          </el-select>
        </el-form-item>

        <el-alert v-if="activeDef?.desc" :title="activeDef.desc" type="info" :closable="false" style="margin-bottom: 18px" />
        <div v-if="!isDNS" class="config-tip" style="margin-bottom: 18px">{{ t('credentials.deployAuthOnly') }}</div>

        <template v-for="field in visibleFields" :key="field.key">
          <el-form-item :label="field.label" :required="field.required">
            <el-select v-if="field.type === 'select'" v-model="form.config[field.key]" style="width: 100%">
              <el-option v-for="option in field.options || []" :key="option.value" :label="option.label" :value="option.value" />
            </el-select>
            <el-input
              v-else-if="field.type === 'textarea'"
              v-model="form.config[field.key]"
              type="textarea"
              :rows="5"
              :placeholder="field.placeholder"
              class="mono"
            />
            <el-input
              v-else-if="field.type === 'password'"
              v-model="form.config[field.key]"
              type="password"
              show-password
              :placeholder="field.placeholder"
            />
            <el-input
              v-else-if="field.type === 'number'"
              v-model="form.config[field.key]"
              :placeholder="field.placeholder"
            />
            <el-input v-else v-model="form.config[field.key]" :placeholder="field.placeholder" />
            <div v-if="field.secret && editingId" class="form-help">{{ t('credentials.secretKeepHint') }}</div>
            <div v-if="field.help" class="form-help">{{ field.help }}</div>
          </el-form-item>
        </template>

        <el-form-item :label="t('credentials.remark')">
          <el-input v-model="form.remark" :placeholder="t('credentials.remarkPlaceholder')" maxlength="100" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('credentials.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ t('credentials.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Close, Delete, Plus, Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { requestSecondaryVerification, secondaryVerifyHeader, withSecondaryVerify } from '../secondaryVerify'
import { loadMeta } from '../store'
import { formatDate, fromNow } from '../utils'
import { providerLogo } from '../providerLogos'
import DnsAccountIcon from '../components/DnsAccountIcon.vue'
import type { Certificate, DeployRecord, FieldDef, Meta, ProviderDef } from '../types'

interface CredentialItem {
  id: number
  name: string
  provider?: string
  type?: string
  config: Record<string, string>
  remark: string
  updated_at: string
}

interface CredentialForm {
  name: string
  type: string
  config: Record<string, string>
  remark: string
}

const route = useRoute()
const { t } = useI18n()
const isDNS = computed(() => route.meta.tab !== 'deploy')

const loading = ref(false)
const saving = ref(false)
const items = ref<CredentialItem[]>([])
const meta = ref<Meta | null>(null)
const records = ref<DeployRecord[]>([])
const certificates = ref<Certificate[]>([])
const dialogVisible = ref(false)
const editingId = ref(0)
const selectedIds = ref<number[]>([])
const batchDeleting = ref(false)

const form = reactive<CredentialForm>({ name: '', type: '', config: {}, remark: '' })

const allSelected = computed(() => items.value.length > 0 && selectedIds.value.length === items.value.length)
const someSelected = computed(() => selectedIds.value.length > 0 && !allSelected.value)

const typeOptions = computed<ProviderDef[]>(() => (isDNS.value ? meta.value?.dns_providers : meta.value?.deploy_types) || [])
const activeDef = computed(() => typeOptions.value.find((item) => item.value === form.type))

const visibleFields = computed<FieldDef[]>(() => {
  const fields = activeDef.value?.fields || []
  return fields.filter((field) => {
    if (!field.show_if) return true
    const source = fields.find((item) => item.key === field.show_if?.key)
    const value = form.config[field.show_if.key] || source?.default || ''
    return value === field.show_if.value
  })
})

const typeValue = computed({
  get: () => form.type,
  set: (value: string) => {
    form.type = value
  }
})

function initials(name: string): string {
  return (name || '?').slice(0, 1).toUpperCase()
}

function logoFor(item: CredentialItem): string {
  return providerLogo(isDNS.value ? item.provider : item.type)
}

function typeLabel(item: CredentialItem): string {
  const value = isDNS.value ? item.provider : item.type
  const list = isDNS.value ? meta.value?.dns_providers : meta.value?.deploy_types
  return (list || []).find((option) => option.value === value)?.label || value || ''
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [metaData, list] = await Promise.all([
      loadMeta(true),
      api.get<CredentialItem[]>(isDNS.value ? '/dns-accounts' : '/deploy-accounts')
    ])
    meta.value = metaData
    items.value = list || []
    selectedIds.value = []
    if (!isDNS.value) await loadRecords()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

async function loadRecords(): Promise<void> {
  try {
    const [deployments, certs] = await Promise.all([
      api.get<DeployRecord[]>('/deployments'),
      api.get<Certificate[]>('/certificates')
    ])
    records.value = deployments || []
    certificates.value = certs || []
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function certDomain(certID: number): string {
  return certificates.value.find((item) => item.id === certID)?.primary_domain || t('credentials.certHash', { id: certID })
}

function defaultConfig(def?: ProviderDef): Record<string, string> {
  const config: Record<string, string> = {}
  for (const field of def?.fields || []) {
    if (field.default) config[field.key] = field.default
    else if (field.type === 'select' && field.options?.length) config[field.key] = field.options[0].value
    else config[field.key] = ''
  }
  return config
}

function openCreate(): void {
  editingId.value = 0
  const def = typeOptions.value[0]
  form.name = ''
  form.type = def?.value || ''
  form.config = defaultConfig(def)
  form.remark = ''
  dialogVisible.value = true
}

function openEdit(item: CredentialItem): void {
  editingId.value = item.id
  form.name = item.name
  form.type = (isDNS.value ? item.provider : item.type) || ''
  form.config = { ...(item.config || {}) }
  // 补齐新版本新增的字段
  for (const [key, value] of Object.entries(defaultConfig(activeDef.value))) {
    if (form.config[key] === undefined) form.config[key] = value
  }
  form.remark = item.remark || ''
  dialogVisible.value = true
}

function onTypeChange(_value: string): void {
  form.config = defaultConfig(activeDef.value)
  if (!isDNS.value && form.type === 'ssh') {
    form.config.port = '22'
  }
}

async function save(): Promise<void> {
  if (!form.name.trim()) {
    ElMessage.warning(t('credentials.nameRequired'))
    return
  }
  if (!form.type) {
    ElMessage.warning(t('credentials.typeRequired'))
    return
  }
  const payload: Record<string, unknown> = { name: form.name.trim(), config: form.config, remark: form.remark }
  if (isDNS.value) payload.provider = form.type
  else payload.type = form.type
  saving.value = true
  try {
    const base = isDNS.value ? '/dns-accounts' : '/deploy-accounts'
    const saved = await withSecondaryVerify('account_write', (headers) =>
      editingId.value
        ? api.put(`${base}/${editingId.value}`, payload, headers)
        : api.post(base, payload, headers)
    )
    if (saved === null) return
    ElMessage.success(t('credentials.saved'))
    dialogVisible.value = false
    load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function handleCommand(command: string, item: CredentialItem): Promise<void> {
  if (command === 'edit') {
    openEdit(item)
    return
  }
  try {
    await ElMessageBox.confirm(t('credentials.deleteConfirm', { name: item.name }), t('credentials.deleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    const base = isDNS.value ? '/dns-accounts' : '/deploy-accounts'
    const deleted = await withSecondaryVerify('account_write', (headers) => api.delete(`${base}/${item.id}`, headers))
    if (deleted === null) return
    ElMessage.success(t('credentials.deleted'))
    load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function isSelected(item: CredentialItem): boolean {
  return selectedIds.value.includes(item.id)
}

function toggleItem(item: CredentialItem, checked: boolean): void {
  if (checked) {
    if (!selectedIds.value.includes(item.id)) selectedIds.value.push(item.id)
  } else {
    selectedIds.value = selectedIds.value.filter((id) => id !== item.id)
  }
}

function toggleAll(value: string | number | boolean): void {
  selectedIds.value = value ? items.value.map((item) => item.id) : []
}

function clearSelection(): void {
  selectedIds.value = []
}

async function batchDelete(): Promise<void> {
  const ids = [...selectedIds.value]
  if (!ids.length) return
  try {
    await ElMessageBox.confirm(t('credentials.batchDeleteConfirm', { count: ids.length }), t('credentials.batchDeleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  const token = await requestSecondaryVerification('account_write')
  if (token === null) return
  const verifyHeaders: Record<string, string> = token ? { [secondaryVerifyHeader]: token } : {}
  batchDeleting.value = true
  let failed = 0
  try {
    for (const id of ids) {
      try {
        await api.delete(`${isDNS.value ? '/dns-accounts' : '/deploy-accounts'}/${id}`, verifyHeaders)
      } catch (error: any) {
        failed++
        const name = items.value.find((item) => item.id === id)?.name || id
        ElMessage.error(`${name}: ${error.message}`)
      }
    }
  } finally {
    batchDeleting.value = false
  }
  if (failed) ElMessage.warning(t('credentials.batchDeleteFailed', { count: failed }))
  else ElMessage.success(t('credentials.batchDeleted', { count: ids.length }))
  await load()
}

onMounted(load)
</script>

<style scoped>
.form-help {
  font-size: 12px;
  color: #94a3b8;
  line-height: 1.6;
  margin-top: 6px;
}

.cred-list {
  padding: 0;
  overflow: hidden;
}

.cred-head-name {
  display: flex;
  align-items: center;
  gap: 10px;
}

.cred-head-name :deep(.el-checkbox) {
  height: auto;
  margin-right: 0;
}

.cred-check {
  flex: none;
  margin-right: 0;
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

.cred-list-head,
.cred-row {
  display: grid;
  grid-template-columns: minmax(0, 2.2fr) minmax(0, 1.5fr) 170px 150px;
  align-items: center;
  gap: 16px;
}

.cred-list-head {
  padding: 12px 20px;
  background: #f8fafc;
  border-bottom: 1px solid var(--kp-border);
  color: var(--kp-muted);
  font-size: 12px;
  font-weight: 600;
}

.cred-head-actions {
  text-align: right;
}

.cred-row {
  padding: 16px 20px;
  border-bottom: 1px solid #f1f5f9;
  transition: background 0.15s ease;
}

.cred-row:last-child {
  border-bottom: 0;
}

.cred-row:hover {
  background: #f8fbff;
}

.cred-row.is-selected {
  background: #f5f9ff;
}

.cred-row.is-selected:hover {
  background: #eef5ff;
}

.cred-cell {
  min-width: 0;
}

.cred-name {
  display: flex;
  align-items: center;
  gap: 12px;
}

.cred-name-text {
  min-width: 0;
}

.cred-name-text .cred-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cred-remark {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cred-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
}

@media (max-width: 900px) {
  .cred-list-head {
    display: none;
  }

  .cred-row {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas:
      'name actions'
      'remark remark'
      'time time';
    row-gap: 8px;
  }

  .cred-name {
    grid-area: name;
  }

  .cred-remark {
    grid-area: remark;
  }

  .cred-time {
    grid-area: time;
  }

  .cred-actions {
    grid-area: actions;
  }
}
</style>
