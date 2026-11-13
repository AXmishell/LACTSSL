<template>
  <div class="auth-shell auth-shell-single">
    <section class="auth-panel">
      <div class="auth-form">
        <div class="mobile-logo">
          <img src="/logo.svg" alt="LACTSSL" />
          <span>LACTSSL</span>
        </div>
        <h2>{{ t('auth.welcome') }}</h2>
        <div class="sub">{{ t('auth.subtitle') }}</div>

        <el-alert
          v-if="needsSetup"
          :title="t('auth.notInitializedTitle')"
          type="info"
          :closable="false"
          show-icon
          style="margin-bottom: 16px"
        >
          <div class="muted" style="line-height: 1.8">{{ t('auth.notInitializedDesc') }}</div>
          <el-button type="primary" style="margin-top: 12px" @click="goInstall">{{ t('auth.goInstall') }}</el-button>
        </el-alert>

        <el-form v-if="!needsSetup" :model="form" label-position="top" @submit.prevent="submit">
          <el-form-item :label="t('auth.email')">
            <el-input v-model="form.email" size="large" placeholder="name@example.com" autocomplete="username">
              <template #prefix><el-icon><Message /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item :label="t('auth.password')">
            <el-input
              v-model="form.password"
              size="large"
              type="password"
              show-password
              :placeholder="t('auth.passwordPlaceholder')"
              autocomplete="current-password"
              @keyup.enter="submit"
            >
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item v-if="totpRequired" :label="t('auth.totp')">
            <el-input
              ref="totpInput"
              v-model="form.totp"
              size="large"
              maxlength="6"
              :placeholder="t('auth.totpPlaceholder')"
              autocomplete="one-time-code"
              @keyup.enter="submit"
            >
              <template #prefix><el-icon><Key /></el-icon></template>
            </el-input>
          </el-form-item>

          <el-alert v-if="errorMessage" :title="errorMessage" type="error" :closable="false" show-icon style="margin-bottom: 16px" />

          <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
            {{ totpRequired ? t('auth.verifyLogin') : t('auth.login') }}
          </el-button>
        </el-form>

        <div v-if="!needsSetup" class="flex-between" style="margin-top: 18px">
          <span class="muted">{{ t('auth.forgot') }}</span>
          <el-button text type="primary" @click="forgotVisible = true">{{ t('auth.resetByEmail') }}</el-button>
        </div>

        <div class="auth-hint">
          {{ t('auth.hint1') }}<br />
          {{ t('auth.hint2') }}
        </div>
      </div>
    </section>

    <el-dialog v-model="forgotVisible" :title="t('auth.resetTitle')" width="420px">
      <p class="muted" style="margin-top: 0; line-height: 1.8">
        {{ t('auth.resetDesc') }}
      </p>
      <el-form label-position="top" @submit.prevent="submitForgot">
        <el-form-item :label="t('auth.accountEmail')">
          <el-input v-model="forgotEmail" placeholder="name@example.com" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="forgotVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="forgotLoading" @click="submitForgot">{{ t('auth.sendReset') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { api, installPath } from '../api'
import { state, loadUser } from '../store'
import { beginLoading, endLoading } from '../loading'
import type { AuthResult, MessageResult, SetupStatus } from '../types'

// 登录成功后保留的过渡加载时长（含 200ms 淡出），只用于 /login → 控制台，
// 不影响后台页面切换。
const LOGIN_LOADING_MS = 900
const LOGIN_LOADING_FADE_MS = 200

const router = useRouter()
const { t } = useI18n()
const loading = ref(false)
const totpRequired = ref(false)
const errorMessage = ref('')
const needsSetup = ref(false)
const totpInput = ref<{ focus: () => void } | null>(null)

const form = ref({ email: '', password: '', totp: '' })

const forgotVisible = ref(false)
const forgotEmail = ref('')
const forgotLoading = ref(false)

onMounted(async () => {
  // 会话在 HttpOnly Cookie 中，直接向服务端确认是否已登录。
  try {
    await loadUser(true)
    router.replace('/dashboard')
    return
  } catch (_) {
    // 未登录，继续显示登录表单
  }
  try {
    const status = await api.get<SetupStatus>('/setup/status')
    if (status.initialized === false) {
      // 不做自动跳转：在登录页给出明确提示和按钮，由用户决定何时进入初始化。
      needsSetup.value = true
    } else if (typeof status.initialized !== 'boolean') {
      errorMessage.value = t('auth.cannotGetStatus')
    }
  } catch (error: any) {
    errorMessage.value = error?.message || t('auth.cannotGetStatus')
  }
})

function goInstall(): void {
  router.push(installPath)
}

async function submit(): Promise<void> {
  if (!form.value.email || !form.value.password) {
    errorMessage.value = t('auth.fillEmailPassword')
    return
  }
  if (totpRequired.value && !form.value.totp) {
    errorMessage.value = t('auth.enterTotp')
    return
  }
  loading.value = true
  errorMessage.value = ''
  try {
    const data = await api.post<AuthResult>('/auth/login', {
      email: form.value.email.trim(),
      password: form.value.password,
      totp: form.value.totp
    })
    state.user = data.user
    state.settings = null
    state.meta = null
    // 登录成功使用全局默认加载层（同样的延迟显示、文案与动画），
    // 仅把展示时长保留为约 900ms；控制台内部切换不套用该时长。
    const startedAt = performance.now()
    beginLoading()
    try {
      await router.replace('/dashboard')
      const remaining = LOGIN_LOADING_MS - LOGIN_LOADING_FADE_MS - (performance.now() - startedAt)
      if (remaining > 0) await new Promise((resolve) => window.setTimeout(resolve, remaining))
    } finally {
      endLoading()
    }
    ElMessage.success(t('auth.loginSuccess'))
  } catch (error: any) {
    if (error.totpRequired) {
      totpRequired.value = true
      errorMessage.value = t('auth.totpRequiredNotice')
      await nextTick()
      totpInput.value?.focus()
    } else {
      errorMessage.value = error.message
    }
  } finally {
    loading.value = false
  }
}

async function submitForgot(): Promise<void> {
  if (!forgotEmail.value) {
    ElMessage.warning(t('auth.emailRequired'))
    return
  }
  forgotLoading.value = true
  try {
    const data = await api.post<MessageResult>('/auth/forgot', { email: forgotEmail.value.trim() })
    ElMessage.success(data.message || t('auth.resetSent'))
    forgotVisible.value = false
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    forgotLoading.value = false
  }
}
</script>
