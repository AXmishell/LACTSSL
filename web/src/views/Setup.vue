<template>
  <div class="auth-shell auth-shell-single">
    <section class="auth-panel">
      <div class="auth-form">
        <div class="mobile-logo">
          <img src="/logo.svg" alt="LACTSSL" />
          <span>LACTSSL</span>
        </div>
        <h2>{{ t('setup.title') }}</h2>
        <div class="sub">{{ t('setup.subtitle') }}</div>

        <div class="setup-steps">
          <span class="setup-step"><b>1</b>{{ t('setup.stepAccount') }}</span>
          <span class="setup-step"><b>2</b>{{ t('setup.stepPlugins') }}</span>
          <span class="setup-step"><b>3</b>{{ t('setup.stepCert') }}</span>
        </div>

        <el-form :model="form" label-position="top" @submit.prevent="submit">
          <el-form-item :label="t('setup.setupToken')">
            <el-input v-model="setupToken" size="large" autocomplete="off" :placeholder="t('setup.setupTokenPlaceholder')">
              <template #prefix><el-icon><Key /></el-icon></template>
            </el-input>
            <div class="form-help">{{ t('setup.setupTokenHint') }}</div>
          </el-form-item>
          <el-form-item :label="t('setup.email')">
            <el-input v-model="form.email" size="large" placeholder="name@example.com" autocomplete="username">
              <template #prefix><el-icon><Message /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item :label="t('setup.password')">
            <el-input v-model="form.password" size="large" type="password" show-password :placeholder="t('setup.passwordPlaceholder')">
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item :label="t('setup.confirm')">
            <el-input
              v-model="form.confirm"
              size="large"
              type="password"
              show-password
              :placeholder="t('setup.confirmPlaceholder')"
              @keyup.enter="submit"
            >
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>

          <el-alert v-if="errorMessage" :title="errorMessage" type="error" :closable="false" show-icon style="margin-bottom: 16px" />

          <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
            {{ t('setup.create') }}
          </el-button>
        </el-form>

        <div class="auth-hint">
          {{ t('setup.hint1') }}<br />
          {{ t('setup.hint2') }}
        </div>

        <el-collapse v-model="advancedOpen" class="install-advanced">
          <el-collapse-item :title="t('setup.configTitle')" name="config">
            <div class="sub" style="margin-bottom: 12px">{{ t('setup.configHint') }}</div>
            <el-alert v-if="configLoadError" :title="configLoadError" type="error" :closable="false" show-icon />
            <el-form v-if="configReady && !configLoadError" :model="configForm" label-position="top" size="small">
              <div class="config-section">{{ t('setup.configNetwork') }}</div>
              <el-form-item :label="t('setup.configAddr')">
                <el-input
                  v-model="configForm.server.addr"
                  placeholder=":8080"
                />
                <div class="form-help">{{ t('setup.configAddrHint') }}</div>
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configHost')">
                  <el-input
                    v-model="configForm.server.host"
                    placeholder="0.0.0.0"
                  />
                </el-form-item>
                <el-form-item :label="t('setup.configPort')">
                  <el-input-number
                    v-model="configForm.server.port"
                    :min="1"
                    :max="65535"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <el-form-item :label="t('setup.configTrustedProxies')">
                <el-input
                  v-model="trustedProxiesText"
                  placeholder="127.0.0.1, 10.0.0.0/8"
                />
                <div class="form-help">{{ t('setup.configTrustedProxiesHint') }}</div>
              </el-form-item>

              <el-divider />
              <div class="config-section">{{ t('setup.configSecurity') }}</div>
              <el-form-item :label="t('setup.configPublicURL')">
                <el-input
                  v-model="configForm.public_url"
                  placeholder="https://ssl.example.com"
                />
                <div class="form-help">{{ t('setup.configPublicURLHint') }}</div>
              </el-form-item>
              <el-form-item>
                <el-switch
                  v-model="configForm.session_persistence"
                  :active-text="t('setup.configSessionPersistence')"
                />
                <div class="form-help">{{ t('setup.configSessionPersistenceHint') }}</div>
              </el-form-item>

              <el-divider />
              <div class="config-section">{{ t('setup.configACME') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.acme.enabled"
                  :active-text="t('setup.configACMEEnabled')"
                />
              </el-form-item>
              <el-form-item :label="t('setup.configACMEAddr')">
                <el-input
                  v-model="configForm.acme.addr"
                  placeholder=":14000"
                />
                <div class="form-help">{{ t('setup.configACMEAddrHint') }}</div>
              </el-form-item>
              <el-form-item :label="t('setup.configACMEBaseURL')">
                <el-input
                  v-model="configForm.acme.base_url"
                  placeholder="https://pki.example.com"
                />
                <div class="form-help">{{ t('setup.configACMEBaseURLHint') }}</div>
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configACMETLSCert')">
                  <el-input
                    v-model="configForm.acme.tls.cert"
                    placeholder="/data/acme.crt"
                  />
                </el-form-item>
                <el-form-item :label="t('setup.configACMETLSKey')">
                  <el-input
                    v-model="configForm.acme.tls.key"
                    placeholder="/data/acme.key"
                  />
                </el-form-item>
              </div>
              <div class="form-help config-help-block">{{ t('setup.configACMETLSHint') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.acme.mtls.enabled"
                  :active-text="t('setup.configACMEMTLS')"
                />
              </el-form-item>
              <el-form-item :label="t('setup.configACMEMTLSClientCA')">
                <el-input
                  v-model="configForm.acme.mtls.client_ca"
                  placeholder="/data/client-ca.crt"
                />
                <div class="form-help">{{ t('setup.configACMEMTLSHint') }}</div>
              </el-form-item>

              <el-divider />
              <div class="config-section">{{ t('setup.configLog') }}</div>
              <div class="config-grid">
                <el-form-item :label="t('setup.configLogLevel')">
                  <el-select v-model="configForm.log.level" style="width: 100%">
                    <el-option label="debug" value="debug" />
                    <el-option label="info" value="info" />
                    <el-option label="warn" value="warn" />
                    <el-option label="error" value="error" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('setup.configLogFormat')">
                  <el-select v-model="configForm.log.format" style="width: 100%">
                    <el-option label="text" value="text" />
                    <el-option label="json" value="json" />
                  </el-select>
                </el-form-item>
              </div>
              <el-form-item>
                <el-switch
                  v-model="configForm.log.also_stdout"
                  :active-text="t('setup.configLogAlsoStdout')"
                />
              </el-form-item>
              <el-form-item>
                <el-switch
                  v-model="configForm.log.access_log"
                  :active-text="t('setup.configLogAccessLog')"
                />
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configLogMaxSizeMB')">
                  <el-input-number
                    v-model="configForm.log.max_size_mb"
                    :min="1"
                    :max="1024"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
                <el-form-item :label="t('setup.configLogMaxBackups')">
                  <el-input-number
                    v-model="configForm.log.max_backups"
                    :min="0"
                    :max="100"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <div class="config-grid">
                <el-form-item :label="t('setup.configLogMaxAgeDays')">
                  <el-input-number
                    v-model="configForm.log.max_age_days"
                    :min="0"
                    :max="3650"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
                <el-form-item>
                  <el-switch
                    v-model="configForm.log.compress"
                    :active-text="t('setup.configLogCompress')"
                  />
                </el-form-item>
              </div>

              <div class="config-sub">{{ t('setup.configLogSystem') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.log.system.enabled"
                  :active-text="t('setup.configLogSystemEnabled')"
                />
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configLogSystemLevel')">
                  <el-select v-model="configForm.log.system.level" style="width: 100%">
                    <el-option label="debug" value="debug" />
                    <el-option label="info" value="info" />
                    <el-option label="warn" value="warn" />
                    <el-option label="error" value="error" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('setup.configLogSystemRetentionDays')">
                  <el-input-number
                    v-model="configForm.log.system.retention_days"
                    :min="1"
                    :max="365"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <el-form-item>
                <el-switch
                  v-model="configForm.log.system.access"
                  :active-text="t('setup.configLogSystemAccess')"
                />
              </el-form-item>
              <el-form-item :label="t('setup.configLogSystemMaxRows')">
                <el-input-number
                  v-model="configForm.log.system.max_rows"
                  :min="100"
                  :max="1000000"
                  :step="1000"
                  controls-position="right"
                  style="width: 100%"
                />
              </el-form-item>

              <el-divider />
              <div class="config-section">{{ t('setup.configUpgrade') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.upgrade.auto"
                  :active-text="t('setup.configUpgradeAuto')"
                />
              </el-form-item>
              <el-form-item>
                <el-switch
                  v-model="configForm.upgrade.auto_optimize"
                  :active-text="t('setup.configUpgradeAutoOptimize')"
                />
              </el-form-item>
              <el-form-item>
                <el-switch
                  v-model="configForm.upgrade.vacuum"
                  :active-text="t('setup.configUpgradeVacuum')"
                />
              </el-form-item>
              <div class="config-sub">{{ t('setup.configUpgradeProgram') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.upgrade.program.enabled"
                  :active-text="t('setup.configUpgradeProgramEnabled')"
                />
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configChannel')">
                  <el-select v-model="configForm.upgrade.program.channel" style="width: 100%">
                    <el-option :label="t('setup.configChannelStable')" value="stable" />
                    <el-option :label="t('setup.configChannelBeta')" value="beta" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('setup.configCheckIntervalHours')">
                  <el-input-number
                    v-model="configForm.upgrade.program.check_interval_hours"
                    :min="1"
                    :max="168"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <el-form-item :label="t('setup.configMaxDownloadMB')">
                <el-input-number
                  v-model="configForm.upgrade.program.max_download_mb"
                  :min="8"
                  :max="2048"
                  controls-position="right"
                  style="width: 100%"
                />
              </el-form-item>

              <el-divider />
              <div class="config-section">{{ t('setup.configPlugins') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.plugins.enabled"
                  :active-text="t('setup.configDNSPlugins')"
                />
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configMemoryLimitMB')">
                  <el-input-number
                    v-model="configForm.plugins.memory_limit_mb"
                    :min="0"
                    :max="4096"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
                <el-form-item :label="t('setup.configTimeoutSeconds')">
                  <el-input-number
                    v-model="configForm.plugins.timeout_seconds"
                    :min="1"
                    :max="600"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <el-form-item :label="t('setup.configMaxOutputKB')">
                <el-input-number
                  v-model="configForm.plugins.max_output_kb"
                  :min="1"
                  :max="1024"
                  controls-position="right"
                  style="width: 100%"
                />
              </el-form-item>

              <div class="config-sub">{{ t('setup.configPluginRepo') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.plugins.repo.enabled"
                  :active-text="t('setup.configPluginRepoEnabled')"
                />
              </el-form-item>
              <el-form-item :label="t('setup.configPluginRepoURL')">
                <el-input
                  v-model="configForm.plugins.repo.url"
                  placeholder="https://github.com/AXmishell/LACTSSL-Plugins/releases/download/plugin-index/index.json"
                />
                <div class="form-help">{{ t('setup.configPluginRepoURLHint') }}</div>
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configChannel')">
                  <el-select v-model="configForm.plugins.repo.channel" style="width: 100%">
                    <el-option :label="t('setup.configChannelStable')" value="stable" />
                    <el-option :label="t('setup.configChannelBeta')" value="beta" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('setup.configCheckIntervalHours')">
                  <el-input-number
                    v-model="configForm.plugins.repo.check_interval_hours"
                    :min="1"
                    :max="168"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <div class="config-grid">
                <el-form-item :label="t('setup.configMaxDownloadMB')">
                  <el-input-number
                    v-model="configForm.plugins.repo.max_download_mb"
                    :min="1"
                    :max="4096"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
                <el-form-item :label="t('setup.configTimeoutSeconds')">
                  <el-input-number
                    v-model="configForm.plugins.repo.timeout_seconds"
                    :min="1"
                    :max="600"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <el-form-item>
                <el-switch
                  v-model="configForm.plugins.repo.auto_update"
                  :active-text="t('setup.configPluginRepoAutoUpdate')"
                />
              </el-form-item>

              <div class="config-sub">{{ t('setup.configDeployPlugins') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.deploy_plugins.enabled"
                  :active-text="t('setup.configDeployPluginsEnabled')"
                />
                <div class="form-help">{{ t('setup.configDeployPluginsHint') }}</div>
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configMemoryLimitMB')">
                  <el-input-number
                    v-model="configForm.deploy_plugins.memory_limit_mb"
                    :min="0"
                    :max="4096"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
                <el-form-item :label="t('setup.configTimeoutSeconds')">
                  <el-input-number
                    v-model="configForm.deploy_plugins.timeout_seconds"
                    :min="1"
                    :max="600"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <el-form-item :label="t('setup.configMaxOutputKB')">
                <el-input-number
                  v-model="configForm.deploy_plugins.max_output_kb"
                  :min="1"
                  :max="1024"
                  controls-position="right"
                  style="width: 100%"
                />
              </el-form-item>

              <div class="config-sub">{{ t('setup.configNotifyPlugins') }}</div>
              <el-form-item>
                <el-switch
                  v-model="configForm.notify_plugins.enabled"
                  :active-text="t('setup.configNotifyPluginsEnabled')"
                />
              </el-form-item>
              <div class="config-grid">
                <el-form-item :label="t('setup.configMemoryLimitMB')">
                  <el-input-number
                    v-model="configForm.notify_plugins.memory_limit_mb"
                    :min="0"
                    :max="4096"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
                <el-form-item :label="t('setup.configTimeoutSeconds')">
                  <el-input-number
                    v-model="configForm.notify_plugins.timeout_seconds"
                    :min="1"
                    :max="600"
                    controls-position="right"
                    style="width: 100%"
                  />
                </el-form-item>
              </div>
              <el-form-item :label="t('setup.configMaxOutputKB')">
                <el-input-number
                  v-model="configForm.notify_plugins.max_output_kb"
                  :min="1"
                  :max="1024"
                  controls-position="right"
                  style="width: 100%"
                />
              </el-form-item>

              <el-alert
                v-if="configSaved"
                :title="t('setup.configRestartRequired')"
                type="warning"
                :closable="false"
                show-icon
                style="margin-bottom: 12px"
              />
              <el-button v-if="configSaved" type="warning" plain style="width: 100%; margin-bottom: 12px" @click="restartService">
                {{ t('setup.configRestartNow') }}
              </el-button>
              <el-button type="primary" plain style="width: 100%" :loading="configSaving" @click="saveConfig">
                {{ t('setup.configSave') }}
              </el-button>
            </el-form>
          </el-collapse-item>

          <el-collapse-item :title="t('setup.advanced')" name="restore">
            <div class="sub" style="margin-bottom: 12px">{{ t('setup.restoreBackupHint') }}</div>
            <el-upload
              ref="restoreUpload"
              :auto-upload="false"
              :show-file-list="false"
              accept=".zip,.db"
              :on-change="onRestoreFileChange"
            >
              <el-button :icon="Upload">{{ t('setup.chooseBackup') }}</el-button>
            </el-upload>
            <div v-if="restoreFileName" class="install-file">
              <el-icon><Document /></el-icon>
              <span>{{ restoreFileName }}</span>
            </div>
            <el-input
              v-model="restorePassword"
              type="password"
              show-password
              autocomplete="off"
              :placeholder="t('setup.restorePasswordPlaceholder')"
              style="margin-top: 12px"
            />
            <div class="form-help">{{ t('setup.restorePasswordHint') }}</div>
            <el-alert v-if="restoreError" :title="restoreError" type="error" :closable="false" show-icon style="margin-top: 12px" />
            <el-button
              type="danger"
              plain
              style="width: 100%; margin-top: 12px"
              :loading="restoring"
              :disabled="!restoreFile"
              @click="restoreBackup"
            >
              {{ t('setup.restoreBackup') }}
            </el-button>
          </el-collapse-item>
        </el-collapse>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UploadFile, UploadInstance } from 'element-plus'
import { Document, Key, Upload } from '@element-plus/icons-vue'
import { api, loginPath } from '../api'
import { state } from '../store'
import type {
  AuthResult,
  MessageResult,
  SetupConfig,
  SetupConfigResult,
  SetupConfigSaveResult,
  SetupStatus
} from '../types'

const router = useRouter()
const { t } = useI18n()
const loading = ref(false)
const errorMessage = ref('')
const setupToken = ref('')
const form = ref({ email: '', password: '', confirm: '' })
const advancedOpen = ref<string[]>([])
const restoreUpload = ref<UploadInstance>()
const restoreFile = ref<File | null>(null)
const restoreFileName = ref('')
const restorePassword = ref('')
const restoring = ref(false)
const restoreError = ref('')
const configForm = ref<SetupConfig>(emptySetupConfig())
const configReady = ref(false)
const configLoadError = ref('')
const configSaving = ref(false)
const configSaved = ref(false)
const trustedProxiesText = ref('')

function setupHeaders(): Record<string, string> {
  return { 'X-LACTSSL-SETUP-TOKEN': setupToken.value.trim() }
}

function requireSetupToken(): boolean {
  if (setupToken.value.trim()) return true
  ElMessage.warning(t('setup.setupTokenRequired'))
  return false
}

function emptySetupConfig(): SetupConfig {
  return {
    server: { addr: '', host: '', port: 8080 },
    trusted_proxies: [],
    public_url: '',
    session_persistence: false,
    acme: {
      enabled: true,
      addr: '',
      base_url: '',
      tls: { cert: '', key: '' },
      mtls: { enabled: false, client_ca: '' }
    },
    log: {
      level: 'info',
      format: 'text',
      also_stdout: true,
      max_size_mb: 20,
      max_backups: 5,
      max_age_days: 30,
      compress: true,
      access_log: true,
      system: {
        enabled: true,
        level: 'info',
        access: false,
        retention_days: 7,
        max_rows: 20000
      }
    },
    upgrade: {
      auto: true,
      auto_optimize: true,
      vacuum: true,
      program: { enabled: true, channel: 'stable', check_interval_hours: 6, max_download_mb: 128 }
    },
    plugins: {
      enabled: true,
      memory_limit_mb: 128,
      timeout_seconds: 60,
      max_output_kb: 64,
      repo: {
        enabled: true,
        url: 'https://github.com/AXmishell/LACTSSL-Plugins/releases/download/plugin-index/index.json',
        channel: 'stable',
        auto_update: false,
        timeout_seconds: 30,
        max_download_mb: 256,
        check_interval_hours: 24
      }
    },
    deploy_plugins: { enabled: false, memory_limit_mb: 128, timeout_seconds: 300, max_output_kb: 64 },
    notify_plugins: { enabled: true, memory_limit_mb: 128, timeout_seconds: 30, max_output_kb: 64 }
  }
}

watch(
  configForm,
  () => {
    // 用户继续编辑后，旧的「需要重启」提示不再准确。
    if (configSaved.value) configSaved.value = false
  },
  { deep: true }
)

onMounted(async () => {
  try {
    const status = await api.get<SetupStatus>('/setup/status')
    if (status.initialized) {
      router.replace(loginPath)
      return
    }
  } catch (_) {
    // 忽略状态探测失败，继续显示安装表单
  }
  await loadConfig()
})

async function loadConfig(): Promise<void> {
  try {
    const data = await api.get<SetupConfigResult>('/setup/config')
    configForm.value = data.config
    trustedProxiesText.value = (data.config.trusted_proxies || []).join(', ')
    configReady.value = true
    configLoadError.value = ''
  } catch (error: any) {
    configLoadError.value = error?.message || t('setup.configLoadFailed')
  }
}

async function saveConfig(): Promise<void> {
  if (!requireSetupToken()) return
  configSaving.value = true
  configSaved.value = false
  const proxies = trustedProxiesText.value
    .split(',')
    .map((item) => item.trim())
    .filter((item) => item !== '')
  try {
    const form = configForm.value
    const data = await api.put<SetupConfigSaveResult>('/setup/config', {
      server: {
        addr: form.server.addr.trim(),
        host: form.server.host.trim(),
        port: form.server.port
      },
      trusted_proxies: proxies,
      public_url: form.public_url.trim(),
      session_persistence: form.session_persistence,
      acme: {
        enabled: form.acme.enabled,
        addr: form.acme.addr.trim(),
        base_url: form.acme.base_url.trim(),
        tls: {
          cert: form.acme.tls.cert.trim(),
          key: form.acme.tls.key.trim()
        },
        mtls: {
          enabled: form.acme.mtls.enabled,
          client_ca: form.acme.mtls.client_ca.trim()
        }
      },
      log: {
        level: form.log.level,
        format: form.log.format,
        also_stdout: form.log.also_stdout,
        max_size_mb: form.log.max_size_mb,
        max_backups: form.log.max_backups,
        max_age_days: form.log.max_age_days,
        compress: form.log.compress,
        access_log: form.log.access_log,
        system: {
          enabled: form.log.system.enabled,
          level: form.log.system.level,
          access: form.log.system.access,
          retention_days: form.log.system.retention_days,
          max_rows: form.log.system.max_rows
        }
      },
      upgrade: {
        auto: form.upgrade.auto,
        auto_optimize: form.upgrade.auto_optimize,
        vacuum: form.upgrade.vacuum,
        program: {
          enabled: form.upgrade.program.enabled,
          channel: form.upgrade.program.channel,
          check_interval_hours: form.upgrade.program.check_interval_hours,
          max_download_mb: form.upgrade.program.max_download_mb
        }
      },
      plugins: {
        enabled: form.plugins.enabled,
        memory_limit_mb: form.plugins.memory_limit_mb,
        timeout_seconds: form.plugins.timeout_seconds,
        max_output_kb: form.plugins.max_output_kb,
        repo: {
          enabled: form.plugins.repo.enabled,
          url: form.plugins.repo.url.trim(),
          channel: form.plugins.repo.channel,
          auto_update: form.plugins.repo.auto_update,
          timeout_seconds: form.plugins.repo.timeout_seconds,
          max_download_mb: form.plugins.repo.max_download_mb,
          check_interval_hours: form.plugins.repo.check_interval_hours
        }
      },
      deploy_plugins: {
        enabled: form.deploy_plugins.enabled,
        memory_limit_mb: form.deploy_plugins.memory_limit_mb,
        timeout_seconds: form.deploy_plugins.timeout_seconds,
        max_output_kb: form.deploy_plugins.max_output_kb
      },
      notify_plugins: {
        enabled: form.notify_plugins.enabled,
        memory_limit_mb: form.notify_plugins.memory_limit_mb,
        timeout_seconds: form.notify_plugins.timeout_seconds,
        max_output_kb: form.notify_plugins.max_output_kb
      }
    }, setupHeaders())
    configSaved.value = !!data.restart_required
    ElMessage.success(data.message || t('setup.configSaved'))
  } catch (error: any) {
    ElMessage.error(error?.message || t('setup.configSaveFailed'))
  } finally {
    configSaving.value = false
  }
}

async function restartService(): Promise<void> {
  if (!requireSetupToken()) return
  try {
    await ElMessageBox.confirm(t('setup.configRestartConfirm'), t('setup.configRestartConfirmTitle'), {
      confirmButtonText: t('setup.configRestartNow'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    await api.post<MessageResult>('/setup/config/restart', {}, setupHeaders())
    ElMessage.success(t('setup.configRestarting'))
    window.setTimeout(() => window.location.replace('/install'), 5000)
  } catch (error: any) {
    ElMessage.error(error?.message || t('setup.configSaveFailed'))
  }
}

async function submit(): Promise<void> {
  if (!requireSetupToken()) return
  if (!form.value.email) {
    errorMessage.value = t('setup.emailRequired')
    return
  }
  if ((form.value.password || '').length < 8) {
    errorMessage.value = t('setup.passwordMin')
    return
  }
  if (form.value.password !== form.value.confirm) {
    errorMessage.value = t('setup.passwordMismatch')
    return
  }
  loading.value = true
  errorMessage.value = ''
  try {
    const data = await api.post<AuthResult>('/setup', {
      email: form.value.email.trim(),
      password: form.value.password
    }, setupHeaders())
    state.user = data.user
    ElMessage.success(t('setup.success'))
    router.replace('/dashboard')
  } catch (error: any) {
    errorMessage.value = error.message
  } finally {
    loading.value = false
  }
}

function onRestoreFileChange(file: UploadFile): void {
  const raw = file.raw
  if (!raw) return
  if (raw.size > 256 * 1024 * 1024) {
    restoreError.value = t('setup.restoreFileTooLarge')
    restoreUpload.value?.clearFiles()
    return
  }
  restoreError.value = ''
  restoreFile.value = raw
  restoreFileName.value = raw.name
}

async function restoreBackup(): Promise<void> {
  if (!restoreFile.value) {
    restoreError.value = t('setup.restoreFileRequired')
    return
  }
  if (!setupToken.value.trim()) {
    restoreError.value = t('setup.setupTokenRequired')
    return
  }
  try {
    await ElMessageBox.confirm(t('setup.restoreConfirm'), t('setup.restoreConfirmTitle'), {
      confirmButtonText: t('setup.restoreConfirmButton'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  restoring.value = true
  restoreError.value = ''
  try {
    const formData = new FormData()
    formData.append('file', restoreFile.value)
    formData.append('backup_password', restorePassword.value)
    const data = await api.post<MessageResult>('/setup/restore', formData, setupHeaders())
    ElMessage.success(data.message || t('setup.restoreUploaded'))
    // 服务重启后由安装页自动判断是否已初始化；如门禁入口来自备份，请查看启动日志。
    window.setTimeout(() => window.location.replace('/install'), 5000)
  } catch (error: any) {
    restoreError.value = error.message
  } finally {
    restoring.value = false
  }
}
</script>

<style scoped>
.setup-steps {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0 0 18px;
}
.setup-step {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 7px 12px;
  border-radius: 999px;
  background: #eef2ff;
  color: #4338ca;
  font-size: 12px;
  line-height: 1.3;
}
.setup-step b {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: #4f46e5;
  color: #fff;
  font-size: 11px;
  font-weight: 600;
}
.install-advanced {
  margin-top: 18px;
  border-top: 1px dashed #e2e8f0;
  padding-top: 8px;
}
.install-advanced :deep(.el-collapse) {
  border: none;
}
.install-advanced :deep(.el-collapse-item__header) {
  background: transparent;
  font-size: 13px;
  color: #64748b;
}
.install-advanced :deep(.el-collapse-item__wrap) {
  background: transparent;
}
.install-file {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  padding: 9px 12px;
  border-radius: 10px;
  background: #f8fafc;
  color: #334155;
  font-size: 13px;
  word-break: break-all;
}
.config-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
}
.config-section {
  font-size: 13px;
  font-weight: 600;
  color: #334155;
  margin-bottom: 10px;
}
.config-sub {
  font-size: 12px;
  font-weight: 600;
  color: #475569;
  margin: 4px 0 10px;
}
.config-help-block {
  margin: -6px 0 12px;
}
/* 运行配置字段较多，适当加宽登录卡片，避免两列输入框过窄。 */
.auth-form {
  max-width: 560px;
}
@media (max-width: 520px) {
  .config-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 6px;
  line-height: 1.6;
}
</style>
