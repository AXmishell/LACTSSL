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

    <div class="k-card" style="padding: 8px 16px 16px">
      <el-table :data="items" v-loading="loading" style="width: 100%">
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

    <div class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ t('ca.genericTitle') }}</h3>
          <div class="sub">{{ t('ca.genericSub') }}</div>
        </div>
        <el-button type="primary" :icon="Plus" @click="openGenericCreate">{{ t('ca.genericAdd') }}</el-button>
      </div>
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
import type { ACMECA, CAConfig, MessageResult, Meta, Settings } from '../types'

interface ACMEForm {
  acme_email: string
  acme_ca: string
  acme_key_type: string
  acme_renew_days: number
}

const { t } = useI18n()
const items = ref<CAConfig[]>([])
const genericItems = ref<ACMECA[]>([])
const meta = ref<Meta | null>(null)
const loading = ref(false)
const genericLoading = ref(false)
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

const acme = reactive<ACMEForm>({
  acme_email: '',
  acme_ca: 'letsencrypt',
  acme_key_type: 'ecdsa',
  acme_renew_days: 30
})

function applySettings(data: Settings): void {
  Object.assign(acme, {
    acme_email: data.acme_email || '',
    acme_ca: data.acme_ca || 'letsencrypt',
    acme_key_type: data.acme_key_type || 'ecdsa',
    acme_renew_days: data.acme_renew_days || 30
  })
}

async function load(): Promise<void> {
  loading.value = true
  genericLoading.value = true
  try {
    const [caItems, generic, metaData, settingData] = await Promise.all([
      api.get<CAConfig[]>('/ca-configs'),
      api.get<ACMECA[]>('/acme-cas'),
      loadMeta(true),
      loadSettings(true)
    ])
    items.value = caItems || []
    genericItems.value = generic || []
    meta.value = metaData
    applySettings(settingData)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
    genericLoading.value = false
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
    if (genericEditingId.value) {
      await api.put(`/acme-cas/${genericEditingId.value}`, payload)
    } else {
      await api.post('/acme-cas', payload)
    }
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
    await api.delete(`/acme-cas/${row.id}`)
    ElMessage.success(t('ca.genericDeleted'))
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function saveACME(): Promise<void> {
  savingACME.value = true
  try {
    await api.put('/settings', {
      acme_email: acme.acme_email,
      acme_ca: acme.acme_ca,
      acme_key_type: acme.acme_key_type,
      acme_renew_days: acme.acme_renew_days
    })
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
    const data = await api.put<MessageResult>(`/ca-configs/${editing.value.value}`, {
      eab_kid: form.eab_kid.trim(),
      eab_hmac: form.eab_hmac.trim()
    })
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
    const data = await api.put<MessageResult>(`/ca-configs/${editing.value.value}`, { clear: true })
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

:deep(.ca-actions-cell .cell) {
  padding: 12px;
  text-align: left;
}

:deep(.ca-actions-cell .cell .el-button) {
  margin-left: 0;
  padding-left: 0;
  justify-content: flex-start;
}
</style>
