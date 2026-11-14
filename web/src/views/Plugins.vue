<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('plugins.title') }}</h1>
        <p>{{ t('plugins.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle :loading="loading" @click="load" />
        <el-button
          type="primary"
          plain
          :loading="rescanning"
          :disabled="!plugins || (!plugins.enabled && !plugins.deploy?.enabled)"
          @click="rescan"
        >
          {{ t('plugins.rescan') }}
        </el-button>
      </div>
    </div>

    <div class="k-card" v-loading="loading">
      <div class="card-head">
        <div>
          <h3>{{ t('plugins.dnsTitle') }}</h3>
          <div class="sub">{{ plugins?.dir || t('common.none') }}</div>
        </div>
        <div class="plugin-head-actions">
          <span class="tag-soft" :class="plugins?.enabled ? 'tag-green' : 'tag-gray'">{{ statusText(plugins?.enabled) }}</span>
          <el-button
            size="small"
            :icon="UploadFilled"
            :disabled="!plugins?.enabled"
            @click="openUpload('dns')"
          >
            {{ t('plugins.upload') }}
          </el-button>
        </div>
      </div>
      <div class="about-line"><span>{{ t('plugins.enabled') }}</span><span class="mono">{{ statusText(plugins?.enabled) }}</span></div>
      <div class="about-line"><span>{{ t('plugins.dir') }}</span><span class="mono">{{ plugins?.dir || t('common.none') }}</span></div>
      <div class="about-line"><span>{{ t('plugins.helpers') }}</span><span class="mono">{{ plugins?.helpers ?? 0 }}</span></div>
      <div class="about-line">
        <span>{{ t('plugins.providers') }}</span>
        <span class="mono">{{ enabledCount }} / {{ providers.length }}</span>
      </div>
      <div class="about-line"><span>{{ t('plugins.memoryLimit') }}</span><span class="mono">{{ plugins?.limits?.memory_limit_mb ?? 0 }} MB</span></div>
      <div class="sub" style="margin-top: 8px">{{ t('plugins.dnsSubtitle') }}</div>
      <div class="sub" style="margin-top: 4px">{{ t('plugins.dnsToggleHint') }}</div>
      <div v-if="providers.length" class="plugin-toolbar">
        <el-input v-model="providerFilter" :placeholder="t('plugins.searchProvider')" clearable class="plugin-search" />
        <div class="plugin-bulk">
          <el-button size="small" :loading="bulkLoading" @click="bulkProviders(true)">{{ t('plugins.enableAll') }}</el-button>
          <el-button size="small" :loading="bulkLoading" @click="bulkProviders(false)">{{ t('plugins.disableAll') }}</el-button>
        </div>
      </div>
      <el-table v-if="providers.length" :data="filteredProviders" size="small" max-height="420" class="plugin-table">
        <el-table-column :label="t('plugins.providerName')" min-width="220">
          <template #default="{ row }">
            <div class="plugin-name">{{ row.name || row.id }}</div>
            <div class="plugin-meta mono">{{ row.id }}<span v-if="row.version"> · {{ formatVersion(row.version) }}</span></div>
          </template>
        </el-table-column>
        <el-table-column :label="t('plugins.providerStatus')" width="120" align="right">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              :loading="!!toggling[row.id]"
              @change="(value: any) => toggleProvider(row, !!value)"
            />
          </template>
        </el-table-column>
      </el-table>
      <div v-else class="sub">{{ t('plugins.noProviders') }}</div>
      <el-alert
        v-if="plugins && !plugins.enabled"
        :title="t('plugins.dnsDisabledHint')"
        type="info"
        :closable="false"
        style="margin-top: 12px"
      />
      <el-alert
        v-if="plugins?.skipped?.length"
        :title="`${t('plugins.skipped')}: ${plugins.skipped.join(', ')}`"
        type="warning"
        :closable="false"
        style="margin-top: 12px"
      />
      <el-alert
        v-if="plugins?.errors?.length"
        :title="plugins.errors.join('；')"
        type="warning"
        :closable="false"
        style="margin-top: 12px"
      />
    </div>

    <div class="k-card" v-loading="loading" style="margin-top: 16px">
      <div class="card-head">
        <div>
          <h3>{{ t('plugins.deployTitle') }}</h3>
          <div class="sub">{{ plugins?.deploy?.dir || t('common.none') }}</div>
        </div>
        <div class="plugin-head-actions">
          <span class="tag-soft" :class="plugins?.deploy?.enabled ? 'tag-green' : 'tag-gray'">
            {{ statusText(plugins?.deploy?.enabled) }}
          </span>
          <el-button
            size="small"
            :icon="UploadFilled"
            :disabled="!plugins?.deploy?.enabled"
            @click="openUpload('deploy')"
          >
            {{ t('plugins.upload') }}
          </el-button>
        </div>
      </div>
      <div class="about-line"><span>{{ t('plugins.enabled') }}</span><span class="mono">{{ statusText(plugins?.deploy?.enabled) }}</span></div>
      <div class="about-line"><span>{{ t('plugins.dir') }}</span><span class="mono">{{ plugins?.deploy?.dir || t('common.none') }}</span></div>
      <div class="about-line"><span>{{ t('plugins.deployHelpers') }}</span><span class="mono">{{ plugins?.deploy?.helpers ?? 0 }}</span></div>
      <div class="about-line">
        <span>{{ t('plugins.deployers') }}</span>
        <span class="mono">{{ enabledDeployCount }} / {{ deployers.length }}</span>
      </div>
      <div class="about-line">
        <span>{{ t('plugins.deployMemoryLimit') }}</span>
        <span class="mono">{{ plugins?.deploy?.limits?.memory_limit_mb ?? 0 }} MB</span>
      </div>
      <div class="about-line">
        <span>{{ t('plugins.deployTimeout') }}</span>
        <span class="mono">{{ plugins?.deploy?.limits?.timeout_seconds ?? 0 }} s</span>
      </div>
      <div class="sub" style="margin-top: 8px">{{ t('plugins.deploySubtitle') }}</div>
      <div class="sub" style="margin-top: 4px">{{ t('plugins.deployToggleHint') }}</div>
      <div v-if="deployers.length" class="plugin-toolbar">
        <el-input v-model="deployerFilter" :placeholder="t('plugins.searchDeployer')" clearable class="plugin-search" />
        <div class="plugin-bulk">
          <el-button size="small" :loading="bulkDeployLoading" @click="bulkDeployers(true)">{{ t('plugins.enableAll') }}</el-button>
          <el-button size="small" :loading="bulkDeployLoading" @click="bulkDeployers(false)">{{ t('plugins.disableAll') }}</el-button>
        </div>
      </div>
      <el-table v-if="deployers.length" :data="filteredDeployers" size="small" max-height="420" class="plugin-table">
        <el-table-column :label="t('plugins.deployerName')" min-width="220">
          <template #default="{ row }">
            <div class="plugin-name">{{ row.name || row.id }}</div>
            <div class="plugin-meta mono">{{ row.id }}<span v-if="row.version"> · {{ formatVersion(row.version) }}</span></div>
          </template>
        </el-table-column>
        <el-table-column :label="t('plugins.providerStatus')" width="120" align="right">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              :loading="!!togglingDeployers[row.id]"
              @change="(value: any) => toggleDeployer(row, !!value)"
            />
          </template>
        </el-table-column>
      </el-table>
      <div v-else class="sub">{{ t('plugins.noDeployers') }}</div>
      <el-alert
        v-if="plugins && !plugins.deploy?.enabled"
        :title="t('plugins.disabledHint')"
        type="info"
        :closable="false"
        style="margin-top: 12px"
      />
      <el-alert
        v-if="plugins?.deploy?.skipped?.length"
        :title="`${t('plugins.skipped')}: ${plugins.deploy.skipped.join(', ')}`"
        type="warning"
        :closable="false"
        style="margin-top: 12px"
      />
      <el-alert
        v-if="plugins?.deploy?.errors?.length"
        :title="plugins.deploy.errors.join('；')"
        type="warning"
        :closable="false"
        style="margin-top: 12px"
      />
    </div>

    <el-dialog v-model="uploadVisible" :title="uploadTitle" width="560px" @closed="resetUpload">
      <el-alert
        v-if="uploadKind === 'deploy'"
        :title="t('plugins.uploadDeployWarning')"
        type="warning"
        show-icon
        :closable="false"
        style="margin-bottom: 16px"
      />
      <el-upload
        ref="uploadRef"
        drag
        :auto-upload="false"
        :show-file-list="false"
        :on-change="onPluginFileChange"
      >
        <el-icon class="el-icon--upload"><UploadFilled /></el-icon>
        <div class="el-upload__text">{{ t('plugins.uploadDrop') }}</div>
        <template #tip>
          <div class="el-upload__tip">{{ t('plugins.uploadTip') }}</div>
        </template>
      </el-upload>
      <div v-if="uploadFile" class="plugin-upload-file">
        <el-icon><Document /></el-icon>
        <span class="mono">{{ uploadFile.name }}</span>
        <span class="sub">{{ formatSize(uploadFile.size) }}</span>
      </div>
      <el-form label-position="top" style="margin-top: 16px">
        <el-form-item :label="t('plugins.uploadName')">
          <el-input
            v-model="uploadName"
            maxlength="128"
            :placeholder="t('plugins.uploadNamePlaceholder')"
          />
          <div class="form-help">{{ t('plugins.uploadNameHelp') }}</div>
        </el-form-item>
        <el-form-item>
          <el-checkbox v-model="uploadOverwrite">{{ t('plugins.uploadOverwrite') }}</el-checkbox>
          <div class="form-help">{{ t('plugins.uploadOverwriteHelp') }}</div>
        </el-form-item>
      </el-form>
      <el-alert :title="t('plugins.uploadSecurityHint')" type="warning" show-icon :closable="false" />
      <template #footer>
        <el-button @click="uploadVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="uploading" @click="submitUpload">
          {{ t('plugins.uploadSubmit') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import type { UploadFile, UploadInstance } from 'element-plus'
import { Document, Refresh, UploadFilled } from '@element-plus/icons-vue'
import { api, errorText } from '../api'
import { withSecondaryVerify } from '../secondaryVerify'
import type { PluginProvider, PluginStatus, PluginUploadResult } from '../types'

const { t } = useI18n()
const plugins = ref<PluginStatus | null>(null)
const loading = ref(false)
const rescanning = ref(false)
const providerFilter = ref('')
const bulkLoading = ref(false)
const toggling = ref<Record<string, boolean>>({})
const deployerFilter = ref('')
const bulkDeployLoading = ref(false)
const togglingDeployers = ref<Record<string, boolean>>({})
const uploadVisible = ref(false)
const uploadKind = ref<'dns' | 'deploy'>('dns')
const uploadRef = ref<UploadInstance>()
const uploadFile = ref<File | null>(null)
const uploadName = ref('')
const uploadOverwrite = ref(false)
const uploading = ref(false)
const uploadTitle = computed(() =>
  uploadKind.value === 'dns' ? t('plugins.uploadDnsTitle') : t('plugins.uploadDeployTitle')
)

const providers = computed(() => plugins.value?.providers || [])
const enabledCount = computed(() => providers.value.filter((item) => item.enabled).length)
const filteredProviders = computed(() => {
  const keyword = providerFilter.value.trim().toLowerCase()
  if (!keyword) return providers.value
  return providers.value.filter(
    (item) => (item.name || '').toLowerCase().includes(keyword) || item.id.includes(keyword)
  )
})

const deployers = computed(() => plugins.value?.deploy?.deployers || [])
const enabledDeployCount = computed(() => deployers.value.filter((item) => item.enabled).length)
const filteredDeployers = computed(() => {
  const keyword = deployerFilter.value.trim().toLowerCase()
  if (!keyword) return deployers.value
  return deployers.value.filter(
    (item) => (item.name || '').toLowerCase().includes(keyword) || item.id.includes(keyword)
  )
})

function statusText(enabled?: boolean): string {
  if (!plugins.value) return t('common.none')
  return enabled ? t('plugins.on') : t('plugins.off')
}

// helper 清单里的 version 可能已经带 v 前缀（例如 lego 的 v4.35.2），
// 展示时只补一次 v，避免出现 vv4.35.2。
function formatVersion(version: string): string {
  const value = version.trim()
  return /^v/i.test(value) ? value : `v${value}`
}

async function load(): Promise<void> {
  loading.value = true
  try {
    plugins.value = await api.get<PluginStatus>('/plugins')
  } catch (error) {
    plugins.value = null
    console.warn('failed to load plugins', error)
  } finally {
    loading.value = false
  }
}

async function toggleProvider(provider: PluginProvider, enabled: boolean): Promise<void> {
  toggling.value[provider.id] = true
  try {
    const result = await api.put<{ enabled: boolean; errors?: string[] }>(
      `/plugins/providers/${encodeURIComponent(provider.id)}`,
      { enabled }
    )
    provider.enabled = result.enabled
    const key = enabled ? 'plugins.providerEnabled' : 'plugins.providerDisabled'
    ElMessage.success(t(key, { name: provider.name || provider.id }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
    await load()
  } finally {
    delete toggling.value[provider.id]
  }
}

async function bulkProviders(enabled: boolean): Promise<void> {
  bulkLoading.value = true
  try {
    const result = await api.put<{ updated: number; errors?: string[] }>('/plugins/providers', { enabled })
    await load()
    ElMessage.success(t('plugins.providersUpdated', { count: result.updated }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    bulkLoading.value = false
  }
}

async function toggleDeployer(deployer: { id: string; name?: string; enabled: boolean }, enabled: boolean): Promise<void> {
  togglingDeployers.value[deployer.id] = true
  try {
    const result = await api.put<{ enabled: boolean; errors?: string[] }>(
      `/plugins/deployers/${encodeURIComponent(deployer.id)}`,
      { enabled }
    )
    deployer.enabled = result.enabled
    const key = enabled ? 'plugins.deployerEnabled' : 'plugins.deployerDisabled'
    ElMessage.success(t(key, { name: deployer.name || deployer.id }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
    await load()
  } finally {
    delete togglingDeployers.value[deployer.id]
  }
}

async function bulkDeployers(enabled: boolean): Promise<void> {
  bulkDeployLoading.value = true
  try {
    const result = await api.put<{ updated: number; errors?: string[] }>('/plugins/deployers', { enabled })
    await load()
    ElMessage.success(t('plugins.deployersUpdated', { count: result.updated }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    bulkDeployLoading.value = false
  }
}

async function rescan(): Promise<void> {
  rescanning.value = true
  try {
    const result = await api.post<{ providers: number; deploy?: { deployers: number } }>('/plugins/rescan')
    await load()
    ElMessage.success(
      t('plugins.rescanned', {
        providers: result.providers,
        deployers: result.deploy?.deployers ?? 0
      })
    )
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    rescanning.value = false
  }
}

function openUpload(kind: 'dns' | 'deploy'): void {
  uploadKind.value = kind
  uploadVisible.value = true
}

function resetUpload(): void {
  uploadFile.value = null
  uploadName.value = ''
  uploadOverwrite.value = false
  uploadRef.value?.clearFiles()
}

function onPluginFileChange(file: UploadFile): void {
  const raw = file.raw
  if (!raw) return
  if (raw.size > 256 * 1024 * 1024) {
    ElMessage.warning(t('plugins.uploadTooLarge'))
    uploadRef.value?.clearFiles()
    uploadFile.value = null
    return
  }
  uploadFile.value = raw
  uploadName.value = raw.name
}

function formatSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KB`
  return `${bytes} B`
}

async function submitUpload(): Promise<void> {
  if (!uploadFile.value) {
    ElMessage.warning(t('plugins.uploadFileRequired'))
    return
  }
  const form = new FormData()
  form.append('kind', uploadKind.value)
  form.append('file', uploadFile.value, uploadFile.value.name)
  const name = uploadName.value.trim()
  if (name) form.append('name', name)
  form.append('overwrite', uploadOverwrite.value ? 'true' : 'false')
  uploading.value = true
  try {
    const result = await withSecondaryVerify('plugin_upload', (headers) =>
      api.post<PluginUploadResult>('/plugins/upload', form, headers)
    )
    // 用户在二次验证对话框中取消时保持上传对话框打开，避免丢失已选文件。
    if (!result) return
    uploadVisible.value = false
    await load()
    ElMessage.success(result.message || t('plugins.uploaded', { file: result.file }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    uploading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.plugin-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}
.plugin-head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.plugin-upload-file {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
  padding: 8px 12px;
  border-radius: 8px;
  background: var(--el-fill-color-light);
  word-break: break-all;
}
.form-help {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.5;
  opacity: 0.65;
}
.plugin-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}
.plugin-search {
  max-width: 260px;
}
.plugin-bulk {
  display: flex;
  gap: 8px;
}
.plugin-table {
  margin-top: 10px;
}
.plugin-name {
  font-weight: 500;
}
.plugin-meta {
  margin-top: 2px;
  font-size: 12px;
  opacity: 0.65;
  word-break: break-all;
}
</style>
