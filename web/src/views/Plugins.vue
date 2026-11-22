<template>
  <div class="page table-page plugin-page" :class="{ 'plugin-page-fill': useFillLayout }">
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
          :disabled="!plugins || (!plugins.enabled && !plugins.deploy?.enabled && !plugins.notify?.enabled)"
          @click="rescan"
        >
          {{ t('plugins.rescan') }}
        </el-button>
      </div>
    </div>

    <div class="k-card plugin-repo-card" v-loading="repoLoading">
      <div class="card-head plugin-repo-head">
        <div class="repo-head-main">
          <h3>{{ t('plugins.repoTitle') }}</h3>
          <div class="sub repo-summary">
            <el-tag v-if="repo" size="small" :type="repoSignature.type" effect="plain">{{ repoSignature.text }}</el-tag>
            <span class="repo-summary-text" :title="repoStatusText">{{ repoStatusText }}</span>
            <span v-if="repo?.enabled && repo.channel" class="repo-summary-sep">{{ repo.channel }}</span>
            <span v-if="repo?.platform || repo?.host_version" class="repo-summary-sep mono">
              {{ repo?.platform || '-' }} · {{ repo?.host_version || '-' }}
            </span>
          </div>
        </div>
        <div class="plugin-head-actions">
          <label class="repo-toggle">
            <span>{{ t('plugins.repoEnable') }}</span>
            <el-switch v-model="repoForm.enabled" />
          </label>
          <el-button size="small" type="primary" :loading="repoSaving" @click="saveRepo">{{ t('common.save') }}</el-button>
          <el-button size="small" :icon="Refresh" :loading="repoRefreshing" :disabled="!repoForm.enabled" @click="refreshRepo">
            {{ t('plugins.repoRefresh') }}
          </el-button>
          <el-button size="small" type="primary" plain :disabled="!repoForm.enabled" @click="openMarket">
            {{ t('plugins.repoBrowse') }}
          </el-button>
          <el-button size="small" text :icon="repoExpanded ? ArrowUp : ArrowDown" @click="repoExpanded = !repoExpanded">
            {{ t('plugins.repoSettings') }}
          </el-button>
        </div>
      </div>
      <el-collapse-transition>
        <div v-show="repoExpanded" class="repo-expanded">
          <el-form class="repo-form" label-position="top" @submit.prevent>
            <el-form-item :label="t('plugins.repoUrl')">
              <el-input v-model="repoForm.url" :placeholder="repo?.url || 'https://.../index.json'" />
            </el-form-item>
            <el-form-item :label="t('plugins.repoProxy')">
              <el-input v-model="repoForm.proxy" clearable :placeholder="repo?.proxy || 'http://127.0.0.1:7890 / socks5://127.0.0.1:1080'" />
              <div class="sub">{{ t('plugins.repoProxyHint') }}</div>
            </el-form-item>
            <div class="repo-inline">
              <el-form-item :label="t('plugins.repoChannel')" class="repo-channel">
                <el-select v-model="repoForm.channel">
                  <el-option label="stable" value="stable" />
                  <el-option label="beta" value="beta" />
                </el-select>
              </el-form-item>
              <el-form-item :label="t('plugins.repoAutoUpdate')" class="repo-auto">
                <el-switch v-model="repoForm.autoUpdate" />
              </el-form-item>
            </div>
            <el-form-item :label="t('plugins.repoPublicKey')" class="repo-key">
              <el-input v-model="repoForm.publicKey" type="password" show-password :placeholder="t('plugins.repoPublicKeyPlaceholder')" />
              <div class="form-help key-status">
                <span v-if="repoKeyInputError" class="key-invalid">{{ repoKeyInputError }}</span>
                <span v-else-if="repoForm.publicKey.trim()">{{ t('plugins.repoPublicKeyInputValid') }}</span>
                <template v-else-if="repo?.public_key_set">
                  <span>{{ t('plugins.repoPublicKeyConfigured') }}</span>
                  <span v-if="repo.public_key_valid && repo.public_key_fingerprint" class="mono">
                    · {{ repo.public_key_fingerprint }}
                  </span>
                  <span v-else class="key-invalid">· {{ t('plugins.repoPublicKeyInvalid') }}</span>
                </template>
                <span v-else>{{ t('plugins.repoPublicKeyUnset') }}</span>
              </div>
            </el-form-item>
          </el-form>
        </div>
      </el-collapse-transition>
      <el-alert v-if="repo?.error" :title="repo.error" type="warning" :closable="false" class="repo-alert" />
      <el-alert
        v-if="repo?.enabled && !repo?.public_key_set"
        :title="t('plugins.repoUnsignedWarning')"
        type="warning"
        :closable="false"
        show-icon
        class="repo-alert"
      />
      <el-alert
        v-if="legacyPlugins.length"
        :title="t('plugins.repoLegacyTitle')"
        type="warning"
        :closable="false"
        class="repo-alert"
      >
        <div v-for="item in legacyPlugins" :key="item.kind + item.file" class="legacy-row">
          <span class="mono">{{ item.kind }} / {{ item.file }}</span>
          <el-button size="small" :loading="legacyMigrating[item.kind]" @click="migrateLegacy(item.kind)">
            {{ t('plugins.repoLegacyMigrate') }}
          </el-button>
        </div>
        <div class="sub">{{ t('plugins.repoLegacyHint') }}</div>
      </el-alert>
    </div>

    <div class="plugin-tabs">
      <el-radio-group v-model="activeTab" size="default">
        <el-radio-button value="dns">{{ t('plugins.dnsTab', { count: providers.length }) }}</el-radio-button>
        <el-radio-button value="deploy">{{ t('plugins.deployTab', { count: deployers.length }) }}</el-radio-button>
        <el-radio-button value="notify">{{ t('plugins.notifyTab', { count: notifyChannels.length }) }}</el-radio-button>
      </el-radio-group>
    </div>

    <div v-if="activeTab === 'dns'" class="k-card plugin-card" v-loading="loading">
      <div class="card-head">
        <div>
          <h3>{{ t('plugins.dnsTitle') }}</h3>
          <div class="sub">{{ t('plugins.dnsSubtitle') }}</div>
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
      <div class="plugin-summary">
        <div v-for="item in activeSummary" :key="item.label" class="plugin-summary-item">
          <span class="plugin-summary-label">{{ item.label }}</span>
          <span class="plugin-summary-value" :class="{ mono: item.mono }" :title="item.title || item.value">{{ item.value }}</span>
        </div>
      </div>
      <div v-if="activePluginStatus" class="plugin-status" :title="activePluginStatus.text">
        <el-alert
          :title="activePluginStatus.text"
          :type="activePluginStatus.type"
          :closable="false"
          show-icon
        />
      </div>
      <div v-if="providers.length" class="plugin-toolbar">
        <el-input v-model="providerFilter" :placeholder="t('plugins.searchProvider')" clearable class="plugin-search" />
        <div class="plugin-bulk">
          <el-button size="small" :loading="bulkLoading" @click="bulkProviders(true)">{{ t('plugins.enableAll') }}</el-button>
          <el-button size="small" :loading="bulkLoading" @click="bulkProviders(false)">{{ t('plugins.disableAll') }}</el-button>
        </div>
      </div>
      <div class="plugin-scroll">
        <PluginGroupTable
          v-if="providers.length || dnsFiles.length"
          :fill="useFillLayout"
          kind="dns"
          :items="providers"
          :files="dnsFiles"
          :keyword="providerFilter"
          :toggling="toggling"
          :deleting="deletingFiles"
          :file-toggling="fileToggling"
          :aggregate-loading="dnsGroupToggling"
          :empty-text="t('plugins.noMatched')"
          :batch-action="batchAction"
          @toggle="toggleProvider"
          @toggle-group="toggleDnsGroup"
          @toggle-items="(items, enabled) => togglePluginItems('dns', items, enabled)"
          @toggle-file="(file, enabled) => togglePluginFile('dns', file, enabled)"
          @delete="deletePluginFile"
          @batch-toggle="batchToggleDnsGroups"
          @batch-delete="batchDeletePluginFiles"
        />
        <div v-else class="plugin-scroll-empty sub">{{ t('plugins.noProviders') }}</div>
      </div>
    </div>

    <div v-if="activeTab === 'deploy'" class="k-card plugin-card" v-loading="loading">
      <div class="card-head">
        <div>
          <h3>{{ t('plugins.deployTitle') }}</h3>
          <div class="sub">{{ t('plugins.deploySubtitle') }}</div>
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
      <div class="plugin-summary">
        <div v-for="item in activeSummary" :key="item.label" class="plugin-summary-item">
          <span class="plugin-summary-label">{{ item.label }}</span>
          <span class="plugin-summary-value" :class="{ mono: item.mono }" :title="item.title || item.value">{{ item.value }}</span>
        </div>
      </div>
      <div v-if="activePluginStatus" class="plugin-status" :title="activePluginStatus.text">
        <el-alert
          :title="activePluginStatus.text"
          :type="activePluginStatus.type"
          :closable="false"
          show-icon
        />
      </div>
      <div v-if="deployers.length" class="plugin-toolbar">
        <el-input v-model="deployerFilter" :placeholder="t('plugins.searchDeployer')" clearable class="plugin-search" />
        <div class="plugin-bulk">
          <el-button size="small" :loading="bulkDeployLoading" @click="bulkDeployers(true)">{{ t('plugins.enableAll') }}</el-button>
          <el-button size="small" :loading="bulkDeployLoading" @click="bulkDeployers(false)">{{ t('plugins.disableAll') }}</el-button>
        </div>
      </div>
      <div class="plugin-scroll">
        <PluginGroupTable
          v-if="deployers.length || deployFiles.length"
          :fill="useFillLayout"
          kind="deploy"
          :items="deployers"
          :files="deployFiles"
          :keyword="deployerFilter"
          :toggling="togglingDeployers"
          :deleting="deletingFiles"
          :file-toggling="fileToggling"
          :aggregate-loading="deployGroupToggling"
          :empty-text="t('plugins.noMatched')"
          :batch-action="batchAction"
          @toggle="toggleDeployer"
          @toggle-group="toggleDeployGroup"
          @toggle-items="(items, enabled) => togglePluginItems('deploy', items, enabled)"
          @toggle-file="(file, enabled) => togglePluginFile('deploy', file, enabled)"
          @delete="deletePluginFile"
          @batch-toggle="batchToggleDeployGroups"
          @batch-delete="batchDeletePluginFiles"
        />
        <div v-else class="plugin-scroll-empty sub">{{ t('plugins.noDeployers') }}</div>
      </div>
    </div>

    <div v-if="activeTab === 'notify'" class="k-card plugin-card" v-loading="loading">
      <div class="card-head">
        <div>
          <h3>{{ t('plugins.notifyTitle') }}</h3>
          <div class="sub">{{ t('plugins.notifySubtitle') }}</div>
        </div>
        <div class="plugin-head-actions">
          <span class="tag-soft" :class="plugins?.notify?.enabled ? 'tag-green' : 'tag-gray'">
            {{ statusText(plugins?.notify?.enabled) }}
          </span>
          <el-button
            size="small"
            :icon="UploadFilled"
            :disabled="!plugins?.notify?.enabled"
            @click="openUpload('notify')"
          >
            {{ t('plugins.upload') }}
          </el-button>
        </div>
      </div>
      <div class="plugin-summary">
        <div v-for="item in activeSummary" :key="item.label" class="plugin-summary-item">
          <span class="plugin-summary-label">{{ item.label }}</span>
          <span class="plugin-summary-value" :class="{ mono: item.mono }" :title="item.title || item.value">{{ item.value }}</span>
        </div>
      </div>
      <div v-if="activePluginStatus" class="plugin-status" :title="activePluginStatus.text">
        <el-alert
          :title="activePluginStatus.text"
          :type="activePluginStatus.type"
          :closable="false"
          show-icon
        />
      </div>
      <div v-if="notifyChannels.length" class="plugin-toolbar">
        <el-input v-model="notifyFilter" :placeholder="t('plugins.searchNotifyChannel')" clearable class="plugin-search" />
        <div class="plugin-bulk">
          <el-button size="small" :loading="bulkNotifyLoading" @click="bulkNotifyChannels(true)">{{ t('plugins.enableAll') }}</el-button>
          <el-button size="small" :loading="bulkNotifyLoading" @click="bulkNotifyChannels(false)">{{ t('plugins.disableAll') }}</el-button>
        </div>
      </div>
      <div class="plugin-scroll">
        <PluginGroupTable
          v-if="notifyChannels.length || notifyFiles.length"
          :fill="useFillLayout"
          kind="notify"
          :items="notifyChannels"
          :files="notifyFiles"
          :keyword="notifyFilter"
          :toggling="togglingNotify"
          :deleting="deletingFiles"
          :file-toggling="fileToggling"
          :aggregate-loading="notifyGroupToggling"
          :empty-text="t('plugins.noMatched')"
          :batch-action="batchAction"
          @toggle="toggleNotifyChannel"
          @toggle-group="toggleNotifyGroup"
          @toggle-items="(items, enabled) => togglePluginItems('notify', items, enabled)"
          @toggle-file="(file, enabled) => togglePluginFile('notify', file, enabled)"
          @delete="deletePluginFile"
          @batch-toggle="batchToggleNotifyGroups"
          @batch-delete="batchDeletePluginFiles"
        />
        <div v-else class="plugin-scroll-empty sub">{{ t('plugins.noNotifyChannels') }}</div>
      </div>
    </div>

    <el-dialog v-model="marketVisible" :title="t('plugins.repoMarketTitle')" width="920px">
      <div class="plugin-toolbar">
        <div class="plugin-bulk">
          <el-select v-model="marketKind" style="width: 140px" @change="loadMarket">
            <el-option :label="t('plugins.repoKindAll')" value="all" />
            <el-option label="DNS" value="dns" />
            <el-option :label="t('plugins.repoKindDeploy')" value="deploy" />
            <el-option :label="t('plugins.repoKindNotify')" value="notify" />
          </el-select>
          <el-input v-model="marketQuery" :placeholder="t('plugins.repoSearch')" clearable style="width: 220px" @keyup.enter="loadMarket" />
        </div>
      </div>
      <div class="table-scroll">
        <el-table v-loading="marketLoading" :data="marketPlugins" size="small" max-height="520" class="plugin-table">
          <el-table-column :label="t('plugins.providerName')" min-width="240">
            <template #default="{ row }">
              <div class="plugin-name">
                <img v-if="row.icon" class="market-icon" :src="row.icon" alt="" loading="lazy" />
                {{ row.name || row.id }}
                <el-tag v-if="row.bundle" size="small" type="success" style="margin-left: 6px">{{ t('plugins.repoBundleTag') }}</el-tag>
              </div>
              <div class="plugin-meta mono">
                {{ row.kind }} / {{ row.id }}
                <template v-if="row.bundle && row.provider_count"> · {{ t('plugins.repoBundleProviders', { count: row.provider_count }) }}</template>
                <span v-if="row.size" :title="t('plugins.repoUncompressedSize')"> · {{ formatSize(row.size) }}</span>
              </div>
              <div v-if="row.description" class="plugin-meta">{{ row.description }}</div>
            </template>
          </el-table-column>
          <el-table-column :label="t('plugins.repoVersion')" width="130">
            <template #default="{ row }">
              <span class="mono">{{ formatVersion(row.version) }}</span>
              <div v-if="row.installed_version" class="plugin-meta">{{ t('plugins.repoInstalledVersion', { version: formatVersion(row.installed_version) }) }}</div>
            </template>
          </el-table-column>
          <el-table-column :label="t('plugins.providerStatus')" width="150">
            <template #default="{ row }">
              <el-tag v-if="!row.compatible" type="info" size="small">{{ t('plugins.repoIncompatible') }}</el-tag>
              <el-tag v-else-if="!row.platform_available" type="info" size="small">{{ t('plugins.repoPlatformUnsupported') }}</el-tag>
              <el-tag v-else-if="marketBusy[row.kind + '/' + row.id]" type="warning" size="small">{{ t('plugins.repoProcessing') }}</el-tag>
              <el-tag v-else-if="row.update_available" type="warning" size="small">{{ t('plugins.repoUpdatable') }}</el-tag>
              <el-tag v-else-if="row.installed" type="success" size="small">{{ t('plugins.repoInstalled') }}</el-tag>
              <el-tag v-else type="info" size="small">{{ t('plugins.repoNotInstalled') }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('plugins.repoAction')" width="190" align="right">
            <template #default="{ row }">
              <el-button
                v-if="row.installed && row.update_available"
                size="small"
                type="primary"
                :loading="marketBusy[row.kind + '/' + row.id]"
                @click="installRepoPlugin(row, true)"
              >{{ t('plugins.repoUpdate') }}</el-button>
              <el-button
                v-else-if="!row.installed"
                size="small"
                type="primary"
                :disabled="!row.compatible || !row.platform_available"
                :loading="marketBusy[row.kind + '/' + row.id]"
                @click="installRepoPlugin(row, false)"
              >{{ t('plugins.repoInstall') }}</el-button>
              <el-button
                v-if="row.installed && !row.update_available"
                size="small"
                :loading="marketBusy[row.kind + '/' + row.id]"
                @click="uninstallRepoPlugin(row)"
              >{{ t('plugins.repoUninstall') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
      <div v-if="!marketLoading && !marketPlugins.length" class="sub">{{ t('plugins.repoNoPlugins') }}</div>
    </el-dialog>

    <el-dialog v-model="uploadVisible" :title="uploadTitle" width="560px" @closed="resetUpload">
      <el-alert
        v-if="uploadKind === 'deploy'"
        :title="t('plugins.uploadDeployWarning')"
        type="warning"
        show-icon
        :closable="false"
        style="margin-bottom: 16px"
      />
      <el-alert
        v-else-if="uploadKind === 'notify'"
        :title="t('plugins.uploadNotifyWarning')"
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
import { computed, h, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UploadFile, UploadInstance } from 'element-plus'
import { ArrowDown, ArrowUp, Document, Refresh, UploadFilled } from '@element-plus/icons-vue'
import { api, errorText } from '../api'
import { notifyChannelLabelKey } from '../notifyChannels'
import { formatSize, formatVersion } from '../utils'
import PluginGroupTable from '../components/PluginGroupTable.vue'
import type { LegacyPluginFile, NotifyPluginChannel, PluginConflict, PluginFile, PluginInstallation, PluginProvider, PluginGroup, PluginRepoInstallResult, PluginRepoItem, PluginRepoStatus, PluginRepoUpdateResult, PluginStatus, PluginUploadResult } from '../types'

const { t } = useI18n()
// 视口足够高时才启用一屏布局；1366×768 等短屏回退为整页滚动 + 表格内滚动。
const FILL_MEDIA = '(min-width: 861px) and (min-height: 800px)'
const fillLayout = ref(window.matchMedia(FILL_MEDIA).matches)
let fillMedia: MediaQueryList | null = null
const plugins = ref<PluginStatus | null>(null)
const loading = ref(false)
const rescanning = ref(false)
const providerFilter = ref('')
const activeTab = ref<'dns' | 'deploy' | 'notify'>('dns')
const bulkLoading = ref(false)
const toggling = ref<Record<string, boolean>>({})
const deployerFilter = ref('')
const bulkDeployLoading = ref(false)
const togglingDeployers = ref<Record<string, boolean>>({})
const uploadVisible = ref(false)
const uploadKind = ref<'dns' | 'deploy' | 'notify'>('dns')
const uploadRef = ref<UploadInstance>()
const uploadFile = ref<File | null>(null)
const uploadName = ref('')
const uploadOverwrite = ref(false)
const uploading = ref(false)
const notifyFilter = ref('')
const bulkNotifyLoading = ref(false)
const togglingNotify = ref<Record<string, boolean>>({})
const dnsGroupToggling = ref<Record<string, boolean>>({})
const deployGroupToggling = ref<Record<string, boolean>>({})
const notifyGroupToggling = ref<Record<string, boolean>>({})
const fileToggling = ref<Record<string, boolean>>({})
const batchAction = ref<'' | 'enable' | 'disable' | 'delete'>('')
const deletingFiles = ref<Record<string, boolean>>({})
const repo = ref<PluginRepoStatus | null>(null)
const repoExpanded = ref(false)
// 展开仓库设置时临时回退为整页滚动，避免表单把表格压到 0 高。
const useFillLayout = computed(() => fillLayout.value && !repoExpanded.value)
const repoLoading = ref(false)
const repoSaving = ref(false)
const repoRefreshing = ref(false)
const legacyPlugins = ref<LegacyPluginFile[]>([])
const legacyMigrating = ref<Record<string, boolean>>({})
const marketVisible = ref(false)
const marketLoading = ref(false)
const marketPlugins = ref<PluginRepoItem[]>([])
const marketKind = ref<'all' | 'dns' | 'deploy' | 'notify'>('all')
const marketQuery = ref('')
const marketBusy = ref<Record<string, boolean>>({})
// 插件安装/更新由服务端同步完成，下载大包可能需要较长时间；市场弹窗打开时
// 静默刷新列表，保证自动更新或其它会话触发的版本变化也能及时反映到状态列。
const MARKET_POLL_MS = 15000
let marketTimer: ReturnType<typeof setInterval> | null = null
let marketPolling = false
const repoForm = ref({
  enabled: true,
  url: '',
  channel: 'stable',
  autoUpdate: false,
  proxy: '',
  publicKey: ''
})
// 公钥输入框不回显已保存的值，这里只对本次输入做格式预览。
function validRepoPublicKey(value: string): boolean {
  try {
    return atob(value.replace(/\s+/g, '')).length === 32
  } catch {
    return false
  }
}

const repoKeyInputError = computed(() => {
  const value = repoForm.value.publicKey.replace(/\s+/g, '')
  if (!value || validRepoPublicKey(value)) return ''
  return t('plugins.repoPublicKeyInvalid')
})

const repoSignature = computed<{ type: 'success' | 'warning' | 'danger' | 'info'; text: string }>(() => {
  if (!repo.value?.public_key_set) {
    return { type: 'info', text: t('plugins.repoSignatureUnconfigured') }
  }
  if (!repo.value.public_key_valid) {
    return { type: 'danger', text: t('plugins.repoSignatureInvalidKey') }
  }
  if (repo.value.signature_verified) {
    return { type: 'success', text: t('plugins.repoSignatureVerified') }
  }
  // 索引尚未加载时（例如网络暂时不可达）不能据此断言签名失败。
  if (repo.value.error && !repo.value.checked_at) {
    return { type: 'warning', text: t('plugins.repoSignatureUnavailable') }
  }
  if (repo.value.checked_at) {
    return { type: 'danger', text: t('plugins.repoSignatureFailed') }
  }
  return { type: 'warning', text: t('plugins.repoSignaturePending') }
})

const repoStatusText = computed(() => {
  if (!repo.value) return '-'
  if (!repo.value.enabled) return t('plugins.off')
  if (repo.value.error) return repo.value.error
  if (!repo.value.checked_at) return t('plugins.repoNotLoaded')
  return t('plugins.repoLoaded', { count: repo.value.plugins, time: repo.value.checked_at })
})
const uploadTitle = computed(() => {
  if (uploadKind.value === 'dns') return t('plugins.uploadDnsTitle')
  if (uploadKind.value === 'deploy') return t('plugins.uploadDeployTitle')
  return t('plugins.uploadNotifyTitle')
})

const providers = computed(() => plugins.value?.providers || [])
const enabledCount = computed(() => providers.value.filter((item) => item.enabled).length)
const dnsFiles = computed(() => plugins.value?.files || [])

const deployers = computed(() => plugins.value?.deploy?.deployers || [])
const enabledDeployCount = computed(() => deployers.value.filter((item) => item.enabled).length)
const deployFiles = computed(() => plugins.value?.deploy?.files || [])

const notifyChannels = computed(() => plugins.value?.notify?.channels || [])
const enabledNotifyCount = computed(() => notifyChannels.value.filter((item) => item.enabled).length)
const notifyFiles = computed(() => plugins.value?.notify?.files || [])

interface PluginSummaryItem {
  label: string
  value: string
  mono?: boolean
  title?: string
}

interface PluginTabStatus {
  type: 'info' | 'warning' | 'error'
  text: string
}

// 当前标签页的一行指标：目录、helper、启用数量与资源限制。
const activeSummary = computed<PluginSummaryItem[]>(() => {
  const status = plugins.value
  if (!status) return []
  if (activeTab.value === 'dns') {
    return [
      { label: t('plugins.dir'), value: status.dir || t('common.none'), mono: true, title: status.dir },
      { label: t('plugins.helpers'), value: String(status.helpers ?? 0) },
      { label: t('plugins.providers'), value: `${enabledCount.value} / ${providers.value.length}` },
      { label: t('plugins.memoryLimit'), value: `${status.limits?.memory_limit_mb ?? 0} MB` }
    ]
  }
  if (activeTab.value === 'deploy') {
    return [
      { label: t('plugins.dir'), value: status.deploy?.dir || t('common.none'), mono: true, title: status.deploy?.dir },
      { label: t('plugins.deployHelpers'), value: String(status.deploy?.helpers ?? 0) },
      { label: t('plugins.deployers'), value: `${enabledDeployCount.value} / ${deployers.value.length}` },
      { label: t('plugins.deployMemoryLimit'), value: `${status.deploy?.limits?.memory_limit_mb ?? 0} MB` },
      { label: t('plugins.deployTimeout'), value: `${status.deploy?.limits?.timeout_seconds ?? 0} s` }
    ]
  }
  return [
    { label: t('plugins.dir'), value: status.notify?.dir || t('common.none'), mono: true, title: status.notify?.dir },
    { label: t('plugins.notifyHelpers'), value: String(status.notify?.helpers ?? 0) },
    { label: t('plugins.notifyChannels'), value: `${enabledNotifyCount.value} / ${notifyChannels.value.length}` },
    { label: t('plugins.notifyMemoryLimit'), value: `${status.notify?.limits?.memory_limit_mb ?? 0} MB` },
    { label: t('plugins.notifyTimeout'), value: `${status.notify?.limits?.timeout_seconds ?? 0} s` }
  ]
})

// 表格上方的单行状态摘要：错误 > 跳过 > 未启用，避免多条 alert 把表头推走。
const activePluginStatus = computed<PluginTabStatus | null>(() => {
  const status = plugins.value
  if (!status) return null
  const current = activeTab.value === 'dns'
    ? {
        enabled: !!status.enabled,
        errors: status.errors || [],
        skipped: status.skipped || [],
        disabledHint: t('plugins.dnsDisabledHint')
      }
    : activeTab.value === 'deploy'
      ? {
          enabled: !!status.deploy?.enabled,
          errors: status.deploy?.errors || [],
          skipped: status.deploy?.skipped || [],
          disabledHint: t('plugins.disabledHint')
        }
      : {
          enabled: !!status.notify?.enabled,
          errors: status.notify?.errors || [],
          skipped: status.notify?.skipped || [],
          disabledHint: t('plugins.notifyDisabledHint')
        }
  if (current.errors.length) {
    return { type: 'error', text: `${t('plugins.loadErrors')}: ${current.errors.join('; ')}` }
  }
  if (current.skipped.length) {
    return { type: 'warning', text: `${t('plugins.skipped')}: ${current.skipped.join(', ')}` }
  }
  if (!current.enabled) {
    return { type: 'info', text: current.disabledHint }
  }
  return null
})

function notifyChannelLabel(channel: NotifyPluginChannel): string {
  const key = notifyChannelLabelKey(channel.id)
  return key ? t(key) : channel.name || channel.id
}

function statusText(enabled?: boolean): string {
  if (!plugins.value) return t('common.none')
  return enabled ? t('plugins.on') : t('plugins.off')
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

async function confirmBulkEnable(count: number): Promise<boolean> {
  if (count <= 10) return true
  try {
    await ElMessageBox.confirm(
      t('plugins.enableAllConfirm', { count }),
      t('plugins.enableAllConfirmTitle'),
      { type: 'warning', confirmButtonText: t('plugins.enableAll'), cancelButtonText: t('common.cancel') }
    )
    return true
  } catch {
    return false
  }
}

async function bulkProviders(enabled: boolean): Promise<void> {
  if (enabled && !(await confirmBulkEnable(providers.value.filter((item) => item.file_enabled !== false).length))) return
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
  if (enabled && !(await confirmBulkEnable(deployers.value.filter((item) => item.file_enabled !== false).length))) return
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

async function toggleNotifyChannel(channel: NotifyPluginChannel, enabled: boolean): Promise<void> {
  togglingNotify.value[channel.id] = true
  try {
    const result = await api.put<{ enabled: boolean; errors?: string[] }>(
      `/plugins/notify-channels/${encodeURIComponent(channel.id)}`,
      { enabled }
    )
    channel.enabled = result.enabled
    const key = enabled ? 'plugins.notifyChannelEnabled' : 'plugins.notifyChannelDisabled'
    ElMessage.success(t(key, { name: notifyChannelLabel(channel) }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
    await load()
  } finally {
    delete togglingNotify.value[channel.id]
  }
}

async function bulkNotifyChannels(enabled: boolean): Promise<void> {
  if (enabled && !(await confirmBulkEnable(notifyChannels.value.filter((item) => item.file_enabled !== false).length))) return
  bulkNotifyLoading.value = true
  try {
    const result = await api.put<{ updated: number; errors?: string[] }>('/plugins/notify-channels', { enabled })
    await load()
    ElMessage.success(t('plugins.notifyChannelsUpdated', { count: result.updated }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    bulkNotifyLoading.value = false
  }
}

type PluginKind = 'dns' | 'deploy' | 'notify'

const pluginGroupEndpoints: Record<PluginKind, string> = {
  dns: '/plugins/providers',
  deploy: '/plugins/deployers',
  notify: '/plugins/notify-channels'
}

const pluginGroupUpdatedKeys: Record<PluginKind, string> = {
  dns: 'plugins.providersUpdated',
  deploy: 'plugins.deployersUpdated',
  notify: 'plugins.notifyChannelsUpdated'
}

// 文件级聚合开关：一次请求启用/停用该 helper 提供的全部条目。
async function togglePluginGroup(
  kind: PluginKind,
  group: PluginGroup,
  enabled: boolean,
  loadingState: Ref<Record<string, boolean>>
): Promise<void> {
  const ids = group.items.filter((item) => item.enabled !== enabled).map((item) => item.id)
  if (!ids.length) return
  loadingState.value[group.key] = true
  try {
    const result = await api.put<{ updated: number; errors?: string[] }>(pluginGroupEndpoints[kind], { enabled, ids })
    await load()
    ElMessage.success(t(pluginGroupUpdatedKeys[kind], { count: result.updated }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    delete loadingState.value[group.key]
  }
}

function toggleDnsGroup(group: PluginGroup, enabled: boolean): Promise<void> {
  return togglePluginGroup('dns', group, enabled, dnsGroupToggling)
}

function toggleDeployGroup(group: PluginGroup, enabled: boolean): Promise<void> {
  return togglePluginGroup('deploy', group, enabled, deployGroupToggling)
}

function toggleNotifyGroup(group: PluginGroup, enabled: boolean): Promise<void> {
  return togglePluginGroup('notify', group, enabled, notifyGroupToggling)
}

// 文件级启用/停用：停用后不再探测或加载该 helper，条目勾选会保留。
// 启用会重新执行第三方二进制。
async function togglePluginFile(kind: PluginKind, file: PluginFile, enabled: boolean): Promise<void> {
  const key = file.file
  fileToggling.value[key] = true
  try {
    const path = `/plugins/files/${encodeURIComponent(kind)}/${encodeURIComponent(file.file)}`
    const result = enabled
      ? await api.put<{ enabled: boolean; errors?: string[] }>(path, { enabled })
      : await api.put<{ enabled: boolean; errors?: string[] }>(path, { enabled })
    if (!result) return
    file.enabled = result.enabled
    ElMessage.success(
      t(enabled ? 'plugins.fileEnabled' : 'plugins.fileDisabled', { file: file.name || file.file })
    )
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
    await load()
  } catch (error) {
    ElMessage.error(errorText(error))
    await load()
  } finally {
    delete fileToggling.value[key]
  }
}

// 二级条目：批量启用/停用同一插件包内选中的条目，一次请求只触发一次重扫。
async function togglePluginItems(kind: PluginKind, items: PluginGroup['items'], enabled: boolean): Promise<void> {
  const ids = items.filter((item) => item.enabled !== enabled).map((item) => item.id)
  if (!ids.length) return
  batchAction.value = enabled ? 'enable' : 'disable'
  try {
    const result = await api.put<{ updated: number; errors?: string[] }>(pluginGroupEndpoints[kind], {
      enabled,
      ids
    })
    await load()
    ElMessage.success(t(pluginGroupUpdatedKeys[kind], { count: result.updated }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    batchAction.value = ''
  }
}

// 批量启用/停用选中的插件：优先切换文件级开关，保留每个包内的条目勾选。
// 未关联文件的孤立条目仍走原来的条目级批量接口。
async function batchTogglePluginGroups(kind: PluginKind, groups: PluginGroup[], enabled: boolean): Promise<void> {
  const files = groups.flatMap((group) =>
    group.file && group.file.enabled !== enabled ? [group.file] : []
  )
  const orphanItems = groups
    .filter((group) => !group.file)
    .flatMap((group) => group.items)
    .filter((item) => item.enabled !== enabled)
  if (!files.length && !orphanItems.length) return
  batchAction.value = enabled ? 'enable' : 'disable'
  const warnings: string[] = []
  const run = async (): Promise<boolean> => {
    for (const file of files) {
      const result = await api.put<{ enabled: boolean; errors?: string[] }>(
        `/plugins/files/${encodeURIComponent(kind)}/${encodeURIComponent(file.file)}`,
        { enabled }
      )
      if (result.errors?.length) warnings.push(...result.errors)
    }
    if (orphanItems.length) {
      const result = await api.put<{ updated: number; errors?: string[] }>(pluginGroupEndpoints[kind], {
        enabled,
        ids: orphanItems.map((item) => item.id)
      })
      if (result.errors?.length) warnings.push(...result.errors)
    }
    return true
  }
  try {
    if (enabled && files.length) {
      const done = await run()
      if (done === null) return
    } else {
      await run()
    }
    await load()
    if (files.length) {
      ElMessage.success(t('plugins.filesUpdated', { count: files.length }))
    }
    if (orphanItems.length) {
      ElMessage.success(t(pluginGroupUpdatedKeys[kind], { count: orphanItems.length }))
    }
    if (warnings.length) ElMessage.warning(warnings.join('；'))
  } catch (error) {
    ElMessage.error(errorText(error))
    await load()
  } finally {
    batchAction.value = ''
  }
}

function batchToggleDnsGroups(groups: PluginGroup[], enabled: boolean): Promise<void> {
  return batchTogglePluginGroups('dns', groups, enabled)
}

function batchToggleDeployGroups(groups: PluginGroup[], enabled: boolean): Promise<void> {
  return batchTogglePluginGroups('deploy', groups, enabled)
}

function batchToggleNotifyGroups(groups: PluginGroup[], enabled: boolean): Promise<void> {
  return batchTogglePluginGroups('notify', groups, enabled)
}

// 批量删除只做一次确认，然后逐个删除选中的 helper 文件。
async function batchDeletePluginFiles(files: PluginFile[]): Promise<void> {
  if (!files.length) return
  try {
    await ElMessageBox.confirm(
      t('plugins.fileBatchDeleteConfirm', { count: files.length }),
      t('plugins.fileBatchDeleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('plugins.fileDelete'),
        cancelButtonText: t('common.cancel')
      }
    )
  } catch {
    return
  }
  batchAction.value = 'delete'
  let failed = 0
  const warnings: string[] = []
  try {
    for (const file of files) {
      deletingFiles.value[file.file] = true
      try {
        const result = await api.delete<{ errors?: string[] }>(
          `/plugins/files/${encodeURIComponent(file.kind)}/${encodeURIComponent(file.file)}`
        )
        if (result?.errors?.length) warnings.push(...result.errors)
      } catch (error) {
        failed++
        ElMessage.error(`${file.file}: ${errorText(error)}`)
      } finally {
        delete deletingFiles.value[file.file]
      }
    }
    if (failed) ElMessage.warning(t('plugins.fileBatchDeleteFailed', { count: failed }))
    else ElMessage.success(t('plugins.fileBatchDeleted', { count: files.length }))
    if (warnings.length) ElMessage.warning(warnings.join('；'))
    await Promise.all([load(), loadRepo()])
  } finally {
    batchAction.value = ''
  }
}

async function deletePluginFile(item: PluginFile): Promise<void> {
  const count = item.items.length
  const message = count
    ? t('plugins.fileDeleteConfirm', { file: item.file, items: count })
    : t('plugins.fileDeleteConfirmEmpty', { file: item.file })
  try {
    await ElMessageBox.confirm(message, t('plugins.fileDeleteTitle'), {
      type: 'warning',
      confirmButtonText: t('plugins.fileDelete'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  deletingFiles.value[item.file] = true
  try {
    const result = await api.delete<{ deleted?: string; items?: string[]; errors?: string[] }>(
        `/plugins/files/${encodeURIComponent(item.kind)}/${encodeURIComponent(item.file)}`
      )
    if (!result) return
    ElMessage.success(t('plugins.fileDeleted', { file: item.file }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
    await Promise.all([load(), loadRepo()])
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    delete deletingFiles.value[item.file]
  }
}

async function rescan(): Promise<void> {
  rescanning.value = true
  try {
    const result = await api.post<{ providers: number; deploy?: { deployers: number }; notify?: { channels: number } }>('/plugins/rescan')
    await load()
    ElMessage.success(
      t('plugins.rescanned', {
        providers: result.providers,
        deployers: result.deploy?.deployers ?? 0,
        channels: result.notify?.channels ?? 0
      })
    )
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    rescanning.value = false
  }
}

function openUpload(kind: 'dns' | 'deploy' | 'notify'): void {
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
    const result = await api.post<PluginUploadResult>('/plugins/upload', form)
    // 保持上传对话框打开，避免丢失已选文件。
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

async function loadRepo(): Promise<void> {
  repoLoading.value = true
  try {
    repo.value = await api.get<PluginRepoStatus>('/plugins/repo')
    repoForm.value = {
      enabled: repo.value.enabled,
      // URL/代理可能含 token 或 user:password，服务端只返回脱敏值。
      // 输入框保持为空，留空即保留已保存配置；只有输入新值才会覆盖。
      url: '',
      channel: repo.value.channel || 'stable',
      autoUpdate: repo.value.auto_update,
      proxy: '',
      publicKey: ''
    }
    await loadLegacy()
  } catch (error) {
    console.warn('failed to load plugin repository', error)
  } finally {
    repoLoading.value = false
  }
}

async function loadLegacy(): Promise<void> {
  try {
    const result = await api.get<{ legacy: LegacyPluginFile[] }>('/plugins/repo/legacy')
    legacyPlugins.value = result.legacy || []
  } catch {
    legacyPlugins.value = []
  }
}

async function saveRepo(): Promise<void> {
  if (repoKeyInputError.value) {
    ElMessage.warning(repoKeyInputError.value)
    return
  }
  repoSaving.value = true
  try {
    const payload: Record<string, any> = {
      enabled: repoForm.value.enabled,
      channel: repoForm.value.channel,
      auto_update: repoForm.value.autoUpdate
    }
    // URL/代理/公钥输入框不回显已有值；留空表示保持原值不变。
    if (repoForm.value.url.trim()) payload.url = repoForm.value.url.trim()
    if (repoForm.value.proxy.trim()) payload.proxy = repoForm.value.proxy.trim()
    if (repoForm.value.publicKey.trim()) payload.public_key = repoForm.value.publicKey.trim()
    const result = await api.put<PluginRepoStatus>('/plugins/repo', payload)
    if (!result) return
    repo.value = result
    repoForm.value.url = ''
    repoForm.value.proxy = ''
    repoForm.value.publicKey = ''
    ElMessage.success(t('plugins.repoSaved'))
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    repoSaving.value = false
  }
}

async function refreshRepo(): Promise<void> {
  repoRefreshing.value = true
  try {
    repo.value = await api.post<PluginRepoStatus>('/plugins/repo/refresh')
    ElMessage.success(t('plugins.repoRefreshed'))
    if (marketVisible.value) await loadMarket()
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    repoRefreshing.value = false
  }
}

function openMarket(): void {
  marketVisible.value = true
}

function marketHasBusy(): boolean {
  return Object.keys(marketBusy.value).length > 0
}

function startMarketPolling(): void {
  if (marketTimer) return
  marketTimer = setInterval(() => {
    void pollMarket()
  }, MARKET_POLL_MS)
}

function stopMarketPolling(): void {
  if (!marketTimer) return
  clearInterval(marketTimer)
  marketTimer = null
}

// 轮询只静默更新市场列表，不触发整页 loading，也不在操作进行中覆盖乐观更新。
async function pollMarket(): Promise<void> {
  if (!marketVisible.value || marketPolling || marketLoading.value || marketHasBusy()) return
  if (document.visibilityState === 'hidden') return
  marketPolling = true
  try {
    await loadMarket(true)
  } finally {
    marketPolling = false
  }
}

// 标签页重新可见时立即补一次刷新，避免后台期间状态已经变化却仍显示旧值。
function onMarketVisibilityChange(): void {
  if (document.visibilityState === 'visible') void pollMarket()
}

async function loadMarket(silent = false): Promise<void> {
  if (!silent) marketLoading.value = true
  try {
    const params: Record<string, any> = {}
    if (marketKind.value !== 'all') params.kind = marketKind.value
    if (marketQuery.value.trim()) params.q = marketQuery.value.trim()
    const result = await api.get<{ plugins: PluginRepoItem[] }>('/plugins/repo/plugins', params)
    marketPlugins.value = result.plugins || []
  } catch (error) {
    // 静默轮询失败时保留现有列表，等待下一次重试，避免状态列突然清空。
    if (!silent) {
      marketPlugins.value = []
      ElMessage.error(errorText(error))
    }
  } finally {
    if (!silent) marketLoading.value = false
  }
}

// applyUpdatedPlugin 用服务端返回的安装记录立即刷新当前行，避免状态列在
// 二次列表请求返回前仍显示“可更新”。
function applyUpdatedPlugin(item: PluginRepoItem, plugin?: PluginInstallation): void {
  if (!plugin) return
  item.installed = true
  item.installed_version = plugin.version
  item.update_available = false
}

function conflictTypeLabel(type: PluginConflict['type']): string {
  switch (type) {
    case 'builtin':
      return t('plugins.conflictTypeBuiltin')
    case 'alias':
      return t('plugins.conflictTypeAlias')
    default:
      return t('plugins.conflictTypeID')
  }
}

function conflictSourceLabel(source?: string): string {
  if (source === 'repo') return t('plugins.fileSourceRepo')
  if (source === 'legacy') return t('plugins.fileSourceLegacy')
  return t('plugins.fileSourceManual')
}

// buildConflictMessage 用 VNode 组装冲突说明，避免把插件名称/文件名当作 HTML 注入。
function buildConflictMessage(conflicts: PluginConflict[], item: PluginRepoItem) {
  const blocks = conflicts.map((conflict) => {
    const lines = [
      h('div', { style: 'font-weight: 600; margin-top: 10px' }, `${conflict.kind}/${conflict.id} · ${conflictTypeLabel(conflict.type)}`)
    ]
    if (conflict.type === 'builtin') {
      lines.push(h('div', { style: 'color: var(--el-color-warning)' }, t('plugins.conflictBuiltinHint')))
      return h('div', {}, lines)
    }
    const existing = conflict.existing
    if (!existing) return h('div', {}, lines)
    const meta = [existing.file]
    if (existing.version) meta.push(t('plugins.repoInstalledVersion', { version: formatVersion(existing.version) }))
    meta.push(conflictSourceLabel(existing.source))
    lines.push(h('div', { style: 'color: var(--el-text-color-secondary)' }, meta.join(' · ')))
    if (existing.uncovered?.length) {
      lines.push(h('div', { style: 'color: var(--el-color-warning)' }, t('plugins.conflictUncovered', { items: existing.uncovered.join(', ') })))
    }
    const usage = existing.usage
    if (usage && ((usage.accounts || 0) + (usage.targets || 0) + (usage.channels || 0) > 0)) {
      lines.push(h('div', { style: 'color: var(--el-color-warning)' }, t('plugins.conflictUsage', {
        accounts: usage.accounts || 0,
        targets: usage.targets || 0,
        channels: usage.channels || 0
      })))
    }
    return h('div', {}, lines)
  })
  return h('div', {}, [
    h('p', { style: 'margin: 0 0 4px' }, t('plugins.conflictIntro', { name: item.name || item.id })),
    ...blocks
  ])
}

async function uninstallConflictFiles(conflicts: PluginConflict[]): Promise<void> {
  const seen = new Set<string>()
  for (const conflict of conflicts) {
    const existing = conflict.existing
    if (!existing || seen.has(existing.file)) continue
    seen.add(existing.file)
    if (existing.source === 'repo' && existing.plugin_id) {
      await api.delete(`/plugins/repo/plugins/${encodeURIComponent(conflict.kind)}/${encodeURIComponent(existing.plugin_id)}`)
    } else {
      await api.delete(`/plugins/files/${encodeURIComponent(conflict.kind)}/${encodeURIComponent(existing.file)}`)
    }
  }
}

// handlePluginConflicts 展示冲突详情；仅当所有冲突都能通过卸载解决时才提供“卸载并重试”。
async function handlePluginConflicts(
  conflicts: PluginConflict[],
  item: PluginRepoItem,
  retry: () => Promise<void>
): Promise<boolean> {
  if (!conflicts.length) return false
  const message = buildConflictMessage(conflicts, item)
  if (conflicts.some((conflict) => conflict.type === 'builtin')) {
    await ElMessageBox.alert(message, t('plugins.conflictTitle'), {
      type: 'error',
      confirmButtonText: t('common.confirm')
    })
    return true
  }
  try {
    await ElMessageBox.confirm(message, t('plugins.conflictTitle'), {
      type: 'warning',
      confirmButtonText: t('plugins.conflictUninstallAndRetry'),
      cancelButtonText: t('common.cancel')
    })
  } catch (_) {
    return true
  }
  try {
    await uninstallConflictFiles(conflicts)
  } catch (error) {
    ElMessage.error(errorText(error))
    return true
  }
  ElMessage.info(t('plugins.conflictResolvedRetry'))
  await retry()
  return true
}

function pluginConflictsOf(error: unknown): PluginConflict[] {
  const conflicts = (error as { data?: { conflicts?: PluginConflict[] } })?.data?.conflicts
  return Array.isArray(conflicts) ? conflicts : []
}

async function installRepoPlugin(item: PluginRepoItem, update: boolean): Promise<void> {
  try {
    await ElMessageBox.confirm(
      t(update ? 'plugins.repoUpdateConfirm' : 'plugins.repoInstallConfirm', { name: item.name || item.id }),
      t(update ? 'plugins.repoUpdateTitle' : 'plugins.repoInstallTitle'),
      { type: 'warning', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') }
    )
  } catch (_) {
    return
  }
  await runRepoInstall(item, update)
}

// runRepoInstall 执行实际安装/更新；retried 防止卸载后仍失败时再次弹出冲突对话框。
async function runRepoInstall(item: PluginRepoItem, update: boolean, retried = false): Promise<void> {
  const key = item.kind + '/' + item.id
  marketBusy.value[key] = true
  try {
    const result = await api.post<PluginRepoInstallResult & PluginRepoUpdateResult>(
      update ? '/plugins/repo/update' : '/plugins/repo/install',
      {
        kind: item.kind,
        id: item.id,
        version: update ? undefined : item.version
      }
    )
    if (!result) return
    if (update) {
      const results = result.results || []
      const target = results.find(
        (entry) => entry.kind.toLowerCase() === item.kind.toLowerCase() && entry.id.toLowerCase() === item.id.toLowerCase()
      )
      const conflictEntry = results.find((entry) => (entry.conflicts?.length || 0) > 0)
      if (!retried && conflictEntry?.conflicts?.length) {
        const handled = await handlePluginConflicts(conflictEntry.conflicts, item, () => runRepoInstall(item, update, true))
        if (handled) return
      }
      const failure = target?.error || results.find((entry) => entry.error)?.error
      if (failure) {
        ElMessage.error(failure)
      } else if (target && target.updated !== false) {
        applyUpdatedPlugin(item, target.plugin)
        ElMessage.success(t('plugins.repoUpdated', { name: item.name || item.id }))
      } else {
        ElMessage.warning(t('plugins.repoUpdateNoChange', { name: item.name || item.id }))
      }
      const scanErrors = results.flatMap((entry) => entry.errors || [])
      if (scanErrors.length) ElMessage.warning(scanErrors.join('；'))
    } else {
      applyUpdatedPlugin(item, result.plugin)
      ElMessage.success(t('plugins.repoInstalledMessage', { name: item.name || item.id }))
      ElMessage.info(t('plugins.repoInstalledDisabledHint', { name: item.name || item.id }))
      if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
    }
    await Promise.all([loadMarket(), load(), loadRepo()])
  } catch (error) {
    const conflicts = pluginConflictsOf(error)
    if (!retried && conflicts.length) {
      const handled = await handlePluginConflicts(conflicts, item, () => runRepoInstall(item, update, true))
      if (handled) return
    }
    ElMessage.error(errorText(error))
    // 失败时也静默刷新一次：若服务端已完成更新（例如响应超时），状态列能立即纠正。
    await loadMarket(true)
  } finally {
    delete marketBusy.value[key]
  }
}

async function uninstallRepoPlugin(item: PluginRepoItem): Promise<void> {
  try {
    await ElMessageBox.confirm(t('plugins.repoUninstallConfirm', { name: item.name || item.id }), t('plugins.repoUninstallTitle'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel')
    })
  } catch (_) {
    return
  }
  const key = item.kind + '/' + item.id
  marketBusy.value[key] = true
  try {
    const result = await api.delete<{ errors?: string[] }>(`/plugins/repo/plugins/${encodeURIComponent(item.kind)}/${encodeURIComponent(item.id)}`)
    if (!result) return
    item.installed = false
    item.installed_version = ''
    item.update_available = false
    ElMessage.success(t('plugins.repoUninstalled', { name: item.name || item.id }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
    await Promise.all([loadMarket(), load(), loadRepo()])
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    delete marketBusy.value[key]
  }
}

async function migrateLegacy(kind: string): Promise<void> {
  legacyMigrating.value[kind] = true
  try {
    const result = await api.post<{ migrated?: string[]; errors?: string[] }>('/plugins/repo/migrate', { kind })
    if (!result) return
    ElMessage.success(t('plugins.repoLegacyMigrated', { count: result.migrated?.length || 0 }))
    if (result.errors?.length) ElMessage.warning(result.errors.join('；'))
    await Promise.all([load(), loadRepo()])
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    delete legacyMigrating.value[kind]
  }
}

watch(marketVisible, (visible) => {
  if (visible) {
    void loadMarket()
    startMarketPolling()
  } else {
    stopMarketPolling()
  }
})

function syncFillLayout(event?: MediaQueryListEvent): void {
  fillLayout.value = event ? event.matches : !!fillMedia?.matches
}

onMounted(() => {
  fillMedia = window.matchMedia(FILL_MEDIA)
  fillMedia.addEventListener('change', syncFillLayout)
  syncFillLayout()
  load()
  loadRepo()
  document.addEventListener('visibilitychange', onMarketVisibilityChange)
})

onUnmounted(() => {
  stopMarketPolling()
  fillMedia?.removeEventListener('change', syncFillLayout)
  fillMedia = null
  document.removeEventListener('visibilitychange', onMarketVisibilityChange)
})
</script>

<style scoped>
.plugin-tabs {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.plugin-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}
.plugin-head-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 10px;
}
.repo-head-main {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 14px;
  min-width: 0;
}
.repo-head-main h3 {
  margin: 0;
  white-space: nowrap;
}
.plugin-repo-head .repo-summary {
  margin-top: 0;
}
.repo-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
  min-width: 0;
}
.repo-summary-text {
  min-width: 0;
  max-width: 520px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.repo-summary-sep {
  color: var(--kp-muted);
  white-space: nowrap;
}
.repo-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-right: 2px;
  padding-right: 12px;
  border-right: 1px solid var(--el-border-color-lighter);
  font-size: 13px;
  color: var(--kp-muted);
  white-space: nowrap;
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
.key-status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.key-invalid {
  color: var(--el-color-danger);
  opacity: 1;
}
.key-fingerprint {
  font-size: 12px;
  opacity: 0.75;
  word-break: break-all;
}
.plugin-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 22px;
  margin-top: 0;
  padding: 6px 14px;
  border-radius: 10px;
  background: var(--el-fill-color-lighter);
}
.plugin-summary-item {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
}
.plugin-summary-label {
  font-size: 12px;
  color: var(--kp-muted);
  white-space: nowrap;
}
.plugin-summary-value {
  max-width: 320px;
  overflow: hidden;
  font-size: 13px;
  color: var(--kp-text);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.plugin-status {
  margin-top: 10px;
}
.plugin-status :deep(.el-alert) {
  padding: 5px 12px;
}
.plugin-status :deep(.el-alert__content) {
  min-width: 0;
  width: 100%;
  overflow: hidden;
}
.plugin-status :deep(.el-alert__title) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.plugin-scroll {
  min-height: 0;
}
.plugin-scroll-empty {
  padding: 12px 0 0;
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
.market-icon {
  width: 22px;
  height: 22px;
  object-fit: contain;
  vertical-align: -6px;
  margin-right: 8px;
  padding: 2px;
  border-radius: 6px;
  background: rgba(148, 163, 184, 0.14);
}
.plugin-meta {
  margin-top: 2px;
  font-size: 12px;
  opacity: 0.65;
  word-break: break-all;
}

.plugin-repo-card {
  margin-bottom: 16px;
  padding: 14px 18px;
}
.plugin-repo-card .plugin-repo-head {
  margin-bottom: 0;
}
.repo-expanded {
  max-height: min(48vh, 460px);
  margin-top: 12px;
  padding: 12px 4px 2px 0;
  overflow-y: auto;
  border-top: 1px solid var(--el-border-color-lighter);
}
.repo-form {
  width: 100%;
  max-width: 760px;
}
.repo-form .el-form-item {
  margin-bottom: 14px;
}
.repo-form :deep(.el-form-item__label) {
  margin-bottom: 6px;
  font-size: 13px;
  line-height: 1.4;
}
.repo-inline {
  display: grid;
  grid-template-columns: minmax(140px, 180px) minmax(0, 1fr);
  gap: 0 16px;
}
.repo-auto :deep(.el-form-item__content) {
  padding-top: 4px;
}
.repo-key {
  margin-bottom: 6px;
}
.repo-alert {
  margin-top: 14px;
}
@media (min-width: 861px) {
  /* 短屏回退：取消整页固定高度，表格恢复 420px 内滚动并允许页面滚动。 */
  .plugin-page:not(.plugin-page-fill) {
    display: block;
    height: auto;
    min-height: 0;
  }
  .plugin-page-fill > .page-head,
  .plugin-page-fill > .plugin-repo-card,
  .plugin-page-fill > .plugin-tabs {
    flex: none;
  }
  .plugin-page-fill .plugin-card {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
  }
  .plugin-page-fill .plugin-card > .card-head,
  .plugin-page-fill .plugin-card > .plugin-summary,
  .plugin-page-fill .plugin-card > .plugin-status,
  .plugin-page-fill .plugin-card > .plugin-toolbar {
    flex: none;
  }
  .plugin-page-fill .plugin-scroll {
    flex: 1 1 auto;
    min-height: 0;
    overflow: hidden;
  }
  .plugin-page-fill .plugin-scroll > * {
    height: 100%;
    min-height: 0;
  }
  /* 一屏布局下文件说明会挤占表体高度，改由删除确认框提示。 */
  .plugin-page-fill .plugin-scroll :deep(.plugin-file-hint) {
    display: none;
  }
}
@media (max-width: 960px) {
  .plugin-repo-card .card-head {
    align-items: flex-start;
    flex-direction: column;
    gap: 10px;
  }
  .plugin-repo-card .plugin-head-actions {
    width: 100%;
    justify-content: flex-start;
  }
}
@media (max-width: 860px) {
  .plugin-card .card-head {
    align-items: flex-start;
    flex-direction: column;
    gap: 10px;
  }
  .plugin-card .plugin-head-actions {
    width: 100%;
    justify-content: flex-start;
  }
  .plugin-summary {
    gap: 6px 16px;
  }
  .plugin-summary-value {
    max-width: 100%;
  }
  .plugin-scroll :deep(.plugin-group-fill) {
    height: auto;
  }
}
@media (max-width: 760px) {
  .repo-inline {
    grid-template-columns: minmax(0, 1fr);
  }
  .repo-toggle {
    margin-right: 0;
    padding-right: 0;
    border-right: none;
  }
  .plugin-summary {
    align-items: flex-start;
    flex-direction: column;
  }
}
.legacy-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 6px 0;
}
</style>
