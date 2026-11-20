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
            <ConfigEditor v-if="configReady && !configLoadError" :model-value="configForm" />
            <el-alert
              v-if="configSaved"
              :title="t('setup.configRestartRequired')"
              type="warning"
              :closable="false"
              show-icon
              style="margin-top: 16px; margin-bottom: 12px"
            />
            <el-button v-if="configSaved" type="warning" plain style="width: 100%; margin-bottom: 12px" @click="restartService">
              {{ t('setup.configRestartNow') }}
            </el-button>
            <el-button
              v-if="configReady && !configLoadError"
              type="primary"
              plain
              style="width: 100%"
              :loading="configSaving"
              @click="saveConfig"
            >
              {{ t('setup.configSave') }}
            </el-button>
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
import ConfigEditor from '../components/ConfigEditor.vue'
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
    rate_limit: { max_keys: 10000, rules: {} },
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
  const proxies = (configForm.value.trusted_proxies || [])
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
      rate_limit: {
        max_keys: form.rate_limit.max_keys,
        rules: form.rate_limit.rules
      },
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
