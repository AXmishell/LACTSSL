<template>
  <div class="auth-shell auth-shell-single">
    <section class="auth-panel">
      <div class="auth-form">
        <div class="mobile-logo">
          <img src="/logo.svg" alt="LACTSSL" />
          <span>LACTSSL</span>
        </div>
        <h2>{{ t('setup.title') }}</h2>

        <!-- 初始化令牌是所有模式共用的门禁，提到分段控件之上。 -->
        <el-form label-position="top" class="install-gate" @submit.prevent>
          <el-form-item :label="t('setup.setupToken')">
            <el-input
              v-model="setupToken"
              size="large"
              autocomplete="off"
              :placeholder="t('setup.setupTokenPlaceholder')"
              @change="loadConfig"
              @keyup.enter="loadConfig"
            >
              <template #prefix><el-icon><Key /></el-icon></template>
            </el-input>
            <div class="form-help">{{ t('setup.setupTokenHint') }}</div>
          </el-form-item>
        </el-form>

        <el-segmented
          block
          class="install-switch"
          :model-value="mode"
          :options="modeOptions"
          @change="switchMode"
        />

        <div v-show="mode === 'account'" class="install-pane install-account">
          <el-form :model="form" label-position="top" @submit.prevent="submit">
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
        </div>

        <div v-show="mode === 'advanced'" class="install-pane">
          <el-tabs v-model="advancedTab" class="install-tabs">
            <el-tab-pane :label="t('setup.configTitle')" name="config">
              <div v-loading="configLoading" class="install-config">
                <div class="sub install-pane-hint">{{ t('setup.configHint') }}</div>
                <el-alert
                  v-if="!setupToken.trim()"
                  :title="t('setup.configNeedToken')"
                  type="info"
                  :closable="false"
                  show-icon
                />
                <el-alert v-else-if="configLoadError" :title="configLoadError" type="error" :closable="false" show-icon />
                <ConfigEditor v-if="configReady && !configLoadError" :model-value="configForm" />
                <div v-if="configReady && !configLoadError" class="config-actions">
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
                  <el-button text class="config-back" @click="switchMode('account')">
                    {{ t('setup.backToAccount') }}
                  </el-button>
                </div>
              </div>
            </el-tab-pane>

            <el-tab-pane :label="t('setup.restoreTab')" name="restore">
              <div class="sub install-pane-hint">{{ t('setup.restoreBackupHint') }}</div>
              <el-alert
                v-if="!setupToken.trim()"
                :title="t('setup.configNeedToken')"
                type="info"
                :closable="false"
                show-icon
                class="install-pane-hint"
              />
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
            </el-tab-pane>
          </el-tabs>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UploadFile, UploadInstance } from 'element-plus'
import { Document, Key, Lock, Message, Upload } from '@element-plus/icons-vue'
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
type InstallMode = 'account' | 'advanced'
const mode = ref<InstallMode>('account')
const advancedTab = ref('config')
const modeOptions = computed(() => [
  { label: t('setup.modeAccount'), value: 'account' },
  { label: t('setup.advanced'), value: 'advanced' }
])
const restoreUpload = ref<UploadInstance>()
const restoreFile = ref<File | null>(null)
const restoreFileName = ref('')
const restorePassword = ref('')
const restoring = ref(false)
const restoreError = ref('')
const configForm = ref<SetupConfig>(emptySetupConfig())
const configReady = ref(false)
const configLoading = ref(false)
const configLoadError = ref('')
const configSaving = ref(false)
const configSaved = ref(false)
// 以最近一次加载/保存后的快照判断是否有未保存修改，切换模式前据此确认。
const configSnapshot = ref('')
const configDirty = computed(
  () => configReady.value && configSnapshot.value !== '' && JSON.stringify(configForm.value) !== configSnapshot.value
)

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
    reset: [],
    server: { addr: '', host: '', port: 8080, client_ip_mode: 'auto', proxy_protocol: false },
    trusted_proxies: [],
    public_url: '',
    session_persistence: false,
    rate_limit: { max_keys: 10000, rules: {}, defaults: {} },
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
      },
      archive: {
        enabled: true,
        compress: true,
        retention_days: 0,
        max_total_mb: 0,
        quota_policy: 'fail'
      }
    },
    upgrade: {
      auto: true,
      auto_optimize: true,
      vacuum: true,
      program: {
        enabled: true,
        channel: 'stable',
        check_interval_hours: 6,
        max_download_mb: 128,
        allow_unsigned: false
      }
    },
    plugins: {
      enabled: true,
      sandbox: 'strict',
      memory_limit_mb: 128,
      timeout_seconds: 60,
      max_output_kb: 64
    },
    deploy_plugins: { enabled: false, sandbox: 'strict', memory_limit_mb: 128, timeout_seconds: 300, max_output_kb: 64 },
    notify_plugins: { enabled: true, sandbox: 'strict', memory_limit_mb: 128, timeout_seconds: 30, max_output_kb: 64 }
  }
}

watch(
  configForm,
  () => {
    // 用户继续编辑后，旧的「需要重启」提示不再准确。
    if (configSaved.value && configDirty.value) configSaved.value = false
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
})

let configRequest = 0

async function loadConfig(): Promise<void> {
  const token = setupToken.value.trim()
  if (!token) {
    configRequest++
    configReady.value = false
    configLoading.value = false
    configLoadError.value = ''
    configSnapshot.value = ''
    return
  }
  const requestId = ++configRequest
  configLoading.value = true
  configLoadError.value = ''
  try {
    const data = await api.get<SetupConfigResult>('/setup/config', undefined, setupHeaders())
    if (requestId !== configRequest) return
    configForm.value = data.config
    if (!configForm.value.reset) configForm.value.reset = []
    configReady.value = true
    configLoadError.value = ''
    // 等 ConfigEditor 挂载并补全默认值后再记录基线，避免自动填充被误判为修改。
    await nextTick()
    if (requestId === configRequest) configSnapshot.value = JSON.stringify(configForm.value)
  } catch (error: any) {
    if (requestId !== configRequest) return
    configReady.value = false
    configSnapshot.value = ''
    configLoadError.value = error?.message || t('setup.configLoadFailed')
  } finally {
    if (requestId === configRequest) configLoading.value = false
  }
}

async function switchMode(next: string | number | boolean): Promise<void> {
  const target = next as InstallMode
  if (target === mode.value) return
  if (mode.value === 'advanced' && configDirty.value) {
    try {
      await ElMessageBox.confirm(t('setup.configDiscardConfirm'), t('setup.configDiscardTitle'), {
        confirmButtonText: t('setup.configDiscardButton'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      })
    } catch (_) {
      return
    }
    // 用户确认放弃后，回滚到上次加载/保存的基线，避免“切走再切回”又看到旧修改。
    if (configSnapshot.value) configForm.value = JSON.parse(configSnapshot.value)
  }
  mode.value = target
  if (target === 'advanced' && setupToken.value.trim() && !configReady.value && !configLoading.value) {
    void loadConfig()
  }
  scrollToTop()
}

function scrollToTop(): void {
  if (typeof window === 'undefined') return
  const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  window.scrollTo({ top: 0, behavior: reduce ? 'auto' : 'smooth' })
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
      reset: form.reset || [],
      server: {
        addr: form.server.addr.trim(),
        host: form.server.host.trim(),
        port: form.server.port,
        client_ip_mode: form.server.client_ip_mode,
        proxy_protocol: form.server.proxy_protocol
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
        },
        archive: {
          enabled: form.log.archive.enabled,
          compress: form.log.archive.compress,
          retention_days: form.log.archive.retention_days,
          max_total_mb: form.log.archive.max_total_mb,
          quota_policy: form.log.archive.quota_policy
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
          max_download_mb: form.upgrade.program.max_download_mb,
          allow_unsigned: form.upgrade.program.allow_unsigned
        }
      },
      plugins: {
        enabled: form.plugins.enabled,
        sandbox: form.plugins.sandbox,
        memory_limit_mb: form.plugins.memory_limit_mb,
        timeout_seconds: form.plugins.timeout_seconds,
        max_output_kb: form.plugins.max_output_kb
      },
      deploy_plugins: {
        enabled: form.deploy_plugins.enabled,
        sandbox: form.deploy_plugins.sandbox,
        memory_limit_mb: form.deploy_plugins.memory_limit_mb,
        timeout_seconds: form.deploy_plugins.timeout_seconds,
        max_output_kb: form.deploy_plugins.max_output_kb
      },
      notify_plugins: {
        enabled: form.notify_plugins.enabled,
        sandbox: form.notify_plugins.sandbox,
        memory_limit_mb: form.notify_plugins.memory_limit_mb,
        timeout_seconds: form.notify_plugins.timeout_seconds,
        max_output_kb: form.notify_plugins.max_output_kb
      }
    }, setupHeaders())
    configSaved.value = !!data.restart_required
    form.reset = []
    configSnapshot.value = JSON.stringify(configForm.value)
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
/* 卡片宽度固定，账户与高级设置通过分段控件切换，避免展开配置时整页横向重排。 */
.auth-form {
  width: 100%;
  max-width: 720px;
  min-width: 0;
}
.auth-form h2 {
  margin-bottom: 18px;
}
/* auth-panel 是 grid 子项，默认 min-width:auto 会被标签页撑出横向滚动。
   同时改为顶部对齐，避免在“账户/配置”两种高度差异很大的面板间切换时标题上下跳动。 */
.auth-panel {
  min-width: 0;
  align-items: flex-start;
  padding-top: 64px;
}
.install-gate {
  margin-bottom: 2px;
}
.install-switch {
  width: 100%;
  margin-bottom: 22px;
  --el-segmented-bg-color: #f1f5f9;
  --el-segmented-item-selected-bg-color: #fff;
  --el-segmented-item-selected-color: var(--kp-text);
  --el-segmented-item-hover-bg-color: #e2e8f0;
}
.install-switch :deep(.el-segmented__item) {
  font-weight: 500;
}
.install-switch :deep(.el-segmented__item-selected) {
  box-shadow: 0 1px 3px rgba(15, 23, 42, 0.12);
}
.install-pane {
  min-width: 0;
}
/* 账户输入固定为 360px 总宽：左右内边距各 15px，文字区约 330px；
   密码、确认密码与创建按钮跟随表单列宽，同为 360px。 */
.install-account {
  max-width: 360px;
}
.install-account :deep(.el-input__wrapper) {
  padding-left: 15px;
  padding-right: 15px;
}
.install-pane-hint {
  margin-bottom: 12px;
}
.install-tabs :deep(.el-tabs__header) {
  margin-bottom: 18px;
}
/* 只加粗外层「运行配置/备份恢复」，与配置内部的分区标签形成层级。 */
.install-tabs > :deep(.el-tabs__header) .el-tabs__item {
  font-size: 15px;
  font-weight: 600;
}
.install-config {
  min-height: 120px;
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
/* 保存/重启操作与上面的配置项拉开层级，避免贴在最后一个输入框下面。 */
.config-actions {
  margin-top: 6px;
  padding-top: 14px;
  border-top: 1px solid var(--kp-border);
}
.config-back {
  width: 100%;
  margin: 8px 0 0;
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 6px;
  line-height: 1.6;
}
@media (max-width: 860px) {
  .auth-panel {
    padding: 24px 20px 32px;
  }
  /* 移动端滚动到配置深处时仍能快速切回账户页。 */
  .install-switch {
    position: sticky;
    top: 0;
    z-index: 5;
    margin-bottom: 14px;
    padding: 8px 0;
    background: #fff;
    box-shadow: 0 6px 8px -8px rgba(15, 23, 42, 0.25);
  }
}
</style>
