<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('ca.title') }}</h1>
        <p>{{ t('ca.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <el-alert
      :title="t('ca.alert')"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 18px"
    />

    <div class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ t('ca.defaultParams') }}</h3>
          <div class="sub">{{ t('ca.defaultParamsSub') }}</div>
        </div>
      </div>
      <el-form :model="acme" label-position="top">
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('ca.acmeEmail')">
            <el-input v-model="acme.acme_email" placeholder="name@example.com" />
            <div class="form-help">{{ t('ca.acmeEmailHelp') }}</div>
          </el-form-item>
          <el-form-item :label="t('ca.defaultCA')">
            <el-select v-model="acme.acme_ca" style="width: 100%">
              <el-option v-for="ca in meta?.cas || []" :key="ca.value" :label="ca.label" :value="ca.value" />
            </el-select>
            <div class="form-help">{{ t('ca.defaultCAHelp') }}</div>
          </el-form-item>
          <el-form-item :label="t('ca.defaultPool')">
            <el-select v-model="acme.acme_ca_pool_id" style="width: 100%">
              <el-option :label="t('ca.poolNone')" :value="0" />
              <el-option v-for="pool in pools" :key="pool.id" :label="pool.name" :value="pool.id" :disabled="!pool.enabled" />
            </el-select>
            <div class="form-help">{{ t('ca.defaultPoolHelp') }}</div>
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

        <el-button type="primary" :loading="savingACME" @click="saveACME">{{ t('ca.saveCerts') }}</el-button>
      </el-form>
    </div>

    <div class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ t('ca.poolTitle') }}</h3>
          <div class="sub">{{ t('ca.poolSub') }}</div>
        </div>
        <el-button type="primary" :icon="Plus" @click="openPoolCreate">{{ t('ca.poolAdd') }}</el-button>
      </div>
      <div class="table-scroll">
        <el-table :data="pools" v-loading="poolLoading" style="width: 100%">
          <el-table-column min-width="200" :label="t('ca.poolName')">
            <template #default="{ row }">
              <div class="ca-title">
                {{ row.name }}
                <span v-if="acme.acme_ca_pool_id === row.id" class="tag-soft tag-blue">{{ t('ca.default') }}</span>
                <span v-if="!row.enabled" class="tag-soft tag-gray">{{ t('common.disabled') }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column width="150" :label="t('ca.poolStrategy')">
            <template #default="{ row }">
              <span class="tag-soft tag-gray">{{ strategyLabel(row.strategy) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="120" :label="t('ca.poolCandidates')">
            <template #default="{ row }">
              <span class="muted">{{ row.items?.filter((item: any) => item.enabled).length || 0 }}</span>
            </template>
          </el-table-column>
          <el-table-column width="150" :label="t('ca.poolOrder')">
            <template #default="{ row }">
              <span class="ca-url">{{ poolOrder(row) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="150" align="left" header-align="left" class-name="ca-actions-cell" :label="t('ca.actions')" fixed="right">
            <template #default="{ row }">
              <el-button text type="primary" size="small" @click="openPoolEdit(row)">{{ t('common.edit') }}</el-button>
              <el-button text type="danger" size="small" @click="deletePool(row)">{{ t('common.delete') }}</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div class="muted" style="padding: 24px 0">{{ t('ca.poolEmpty') }}</div>
          </template>
        </el-table>
      </div>
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
            <div v-for="(item, index) in poolForm.items" :key="index" class="flex gap-10" style="margin-bottom: 8px; align-items: center">
              <el-select v-model="item.ca_value" filterable style="flex: 1" :placeholder="t('ca.poolSelectCA')">
                <el-option v-for="ca in meta?.cas || []" :key="ca.value" :label="ca.label" :value="ca.value" />
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

    <div class="k-card" style="padding: 0; overflow: hidden">
      <div class="table-scroll">
        <el-table :data="items" v-loading="loading" class="ca-table" style="width: 100%">
          <el-table-column min-width="260" :label="t('ca.ca')">
            <template #default="{ row }">
              <div class="ca-title-wrap">
                <div>
                  <div class="ca-title">
                    {{ row.label }}
                    <span v-if="row.is_default" class="tag-soft tag-blue">{{ t('ca.default') }}</span>
                  </div>
                  <div class="ca-desc">{{ row.desc }}</div>
                </div>
              </div>
            </template>
          </el-table-column>
          <el-table-column width="120" align="left" header-align="left" class-name="ca-actions-cell" :label="t('ca.actions')" fixed="right">
            <template #default="{ row }">
              <el-button v-if="row.eab" text type="primary" size="small" @click="openConfig(row)">{{ t('ca.configureEAB') }}</el-button>
              <span v-else class="muted">{{ t('ca.noConfigNeeded') }}</span>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </div>

    <div class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ t('ca.genericTitle') }}</h3>
          <div class="sub">{{ t('ca.genericSub') }}</div>
        </div>
        <el-button type="primary" :icon="Plus" @click="openGenericCreate">{{ t('ca.genericAdd') }}</el-button>
      </div>
      <div class="table-scroll">
        <el-table :data="genericItems" v-loading="genericLoading" style="width: 100%">
          <el-table-column min-width="180" :label="t('ca.genericName')">
            <template #default="{ row }">
              <div class="ca-title">
                {{ row.name }}
                <span v-if="acme.acme_ca === `acme-ca:${row.id}`" class="tag-soft tag-blue">{{ t('ca.default') }}</span>
                <span v-if="row.eab_required" class="tag-soft tag-blue">EAB</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column min-width="280" :label="t('ca.genericURL')">
            <template #default="{ row }">
              <span class="ca-url">{{ row.directory_url }}</span>
            </template>
          </el-table-column>
          <el-table-column width="110" :label="t('ca.genericProtocol')">
            <template #default="{ row }">
              <span class="tag-soft tag-gray">{{ String(row.protocol || 'v2').toUpperCase() }}</span>
            </template>
          </el-table-column>
          <el-table-column width="150" align="left" header-align="left" class-name="ca-actions-cell" :label="t('ca.actions')" fixed="right">
            <template #default="{ row }">
              <el-button text type="primary" size="small" @click="openGenericEdit(row)">{{ t('ca.genericEdit') }}</el-button>
              <el-button text type="danger" size="small" @click="deleteGeneric(row)">{{ t('ca.genericDelete') }}</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div class="muted" style="padding: 24px 0">{{ t('ca.genericEmpty') }}</div>
          </template>
        </el-table>
      </div>
    </div>

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
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { loadMeta, loadSettings } from '../store'
import type { ACMECA, CAConfig, CAPool, CAPoolItem, CAPoolStrategy, MessageResult, Meta, Settings } from '../types'

interface ACMEForm {
  acme_email: string
  acme_ca: string
  acme_ca_pool_id: number
  acme_key_type: string
  acme_renew_days: number
}

const { t } = useI18n()
const items = ref<CAConfig[]>([])
const genericItems = ref<ACMECA[]>([])
const pools = ref<CAPool[]>([])
const meta = ref<Meta | null>(null)
const loading = ref(false)
const genericLoading = ref(false)
const poolLoading = ref(false)
const poolSaving = ref(false)
const poolDialogVisible = ref(false)
const poolEditingId = ref<number | null>(null)
const saving = ref(false)
const savingACME = ref(false)
const dialogVisible = ref(false)
const editing = ref<CAConfig | null>(null)

const genericDialogVisible = ref(false)
const genericSaving = ref(false)
const genericEditingId = ref<number | null>(null)
const genericEditing = ref<ACMECA | null>(null)

const form = reactive({ eab_kid: '', eab_hmac: '' })
const genericForm = reactive({
  name: '',
  directory_url: '',
  protocol: 'v2',
  eab_required: false,
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

function applySettings(data: Settings): void {
  Object.assign(acme, {
    acme_email: data.acme_email || '',
    acme_ca: data.acme_ca || 'letsencrypt',
    acme_ca_pool_id: data.acme_ca_pool_id || 0,
    acme_key_type: data.acme_key_type || 'ecdsa',
    acme_renew_days: data.acme_renew_days || 30
  })
}

async function load(): Promise<void> {
  loading.value = true
  genericLoading.value = true
  poolLoading.value = true
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
    genericLoading.value = false
    poolLoading.value = false
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

function poolOrder(pool: CAPool): string {
  return (pool.items || [])
    .filter((item) => item.enabled)
    .sort((a, b) => a.priority - b.priority)
    .map((item) => caOptionLabel(item.ca_value))
    .join(' → ')
}

function caOptionLabel(value: string): string {
  return (meta.value?.cas || []).find((item) => item.value === value)?.label || value
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
    eab_required: false,
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

function onGenericProtocolChange(value: string): void {
  if (value === 'v1') {
    genericForm.eab_required = false
  }
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
  savingACME.value = true
  try {
    const saved = await api.put('/settings', {
        acme_email: acme.acme_email,
        acme_ca: acme.acme_ca,
        acme_ca_pool_id: acme.acme_ca_pool_id || 0,
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
.ca-title-wrap {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.ca-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  font-size: 14px;
  color: #0f172a;
}
.ca-desc {
  margin-top: 3px;
  color: #94a3b8;
  font-size: 12px;
  line-height: 1.5;
}
.ca-url {
  color: #475569;
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  word-break: break-all;
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 6px;
  line-height: 1.6;
}

:deep(.ca-table .cell) {
  padding: 0 22px;
}

:deep(.ca-actions-cell .cell) {
  text-align: left;
}

:deep(.ca-actions-cell .cell .el-button) {
  margin-left: 0;
  padding-left: 0;
  justify-content: flex-start;
}
</style>
