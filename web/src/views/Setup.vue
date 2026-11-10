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

        <el-collapse v-model="advancedOpen" class="install-advanced">
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
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UploadFile, UploadInstance } from 'element-plus'
import { Document, Upload } from '@element-plus/icons-vue'
import { api, loginPath } from '../api'
import { state } from '../store'
import type { AuthResult, MessageResult, SetupStatus } from '../types'

const router = useRouter()
const { t } = useI18n()
const loading = ref(false)
const errorMessage = ref('')
const form = ref({ email: '', password: '', confirm: '' })
const advancedOpen = ref<string[]>([])
const restoreUpload = ref<UploadInstance>()
const restoreFile = ref<File | null>(null)
const restoreFileName = ref('')
const restorePassword = ref('')
const restoring = ref(false)
const restoreError = ref('')

onMounted(async () => {
  try {
    const status = await api.get<SetupStatus>('/setup/status')
    if (status.initialized) router.replace(loginPath)
  } catch (_) {
    // 忽略
  }
})

async function submit(): Promise<void> {
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
    })
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
    const data = await api.post<MessageResult>('/setup/restore', formData)
    ElMessage.success(data.message || t('setup.restoreUploaded'))
    // 服务重启后由安装页自动判断是否已初始化；如安全入口来自备份，请查看启动日志。
    window.setTimeout(() => window.location.replace('/install'), 5000)
  } catch (error: any) {
    restoreError.value = error.message
  } finally {
    restoring.value = false
  }
}
</script>

<style scoped>
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
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 6px;
  line-height: 1.6;
}
</style>
