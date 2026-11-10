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
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { loadMeta, loadSettings } from '../store'
import type { CAConfig, MessageResult, Meta, Settings } from '../types'

interface ACMEForm {
  acme_email: string
  acme_ca: string
  acme_key_type: string
  acme_renew_days: number
}

const { t } = useI18n()
const items = ref<CAConfig[]>([])
const meta = ref<Meta | null>(null)
const loading = ref(false)
const saving = ref(false)
const savingACME = ref(false)
const dialogVisible = ref(false)
const editing = ref<CAConfig | null>(null)

const form = reactive({ eab_kid: '', eab_hmac: '' })

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
  try {
    const [caItems, metaData, settingData] = await Promise.all([
      api.get<CAConfig[]>('/ca-configs'),
      loadMeta(true),
      loadSettings(true)
    ])
    items.value = caItems || []
    meta.value = metaData
    applySettings(settingData)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
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
