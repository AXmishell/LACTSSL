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

          <el-alert
            v-if="errorMessage"
            :title="errorMessage"
            :type="totpNotice ? 'success' : 'error'"
            :closable="false"
            :show-icon="!totpNotice"
            style="margin-bottom: 16px"
          />

          <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
            {{ totpRequired ? t('auth.verifyLogin') : t('auth.login') }}
          </el-button>
        </el-form>

        <div v-if="!needsSetup && passkeyAvailable" class="passkey-login">
          <div class="passkey-divider"><span>{{ t('auth.or') }}</span></div>
          <el-button
            size="large"
            style="width: 100%"
            :loading="passkeyLoading"
            :disabled="!passkeyUsable"
            @click="submitPasskey"
          >
            <el-icon style="margin-right: 6px"><Key /></el-icon>
            {{ t('auth.passkeyLogin') }}
          </el-button>
          <div v-if="!passkeyUsable" class="form-help">{{ t('settings.passkeyUnavailable') }}</div>
          <div v-else class="form-help">{{ t('auth.passkeyLoginHint') }}</div>
        </div>

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
import { onMounted, ref, nextTick, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { api, installPath } from '../api'
import { state, loadUser } from '../store'
import { beginLoading, endLoading } from '../loading'
import { isPasskeyCancelled, loginWithPasskey, passkeySecureContext, passkeySupported } from '../passkeys'
import type { AuthResult, MessageResult, SetupStatus } from '../types'

// 登录成功后保留的过渡加载时长（含 200ms 淡出），只用于 /login → 控制台，
// 不影响后台页面切换。
const LOGIN_LOADING_MS = 900
const LOGIN_LOADING_FADE_MS = 200

const router = useRouter()
const { t } = useI18n()
const loading = ref(false)
const passkeyLoading = ref(false)
const passkeyAvailable = computed(() => passkeySupported())
const passkeyUsable = computed(() => passkeySupported() && passkeySecureContext())
const totpRequired = ref(false)
const errorMessage = ref('')
// 两步验证提示使用绿色提示样式，其余错误仍使用红色错误样式。
const totpNotice = ref(false)
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
      showError(t('auth.cannotGetStatus'))
    }
  } catch (error: any) {
    showError(error?.message || t('auth.cannotGetStatus'))
  }
})

function goInstall(): void {
  router.push(installPath)
}

// 普通错误提示：红色错误样式。
function showError(message: string): void {
  totpNotice.value = false
  errorMessage.value = message
}

// 两步验证提示：绿色提示样式，不带图标。
function showTotpNotice(message: string): void {
  totpNotice.value = true
  errorMessage.value = message
}

function clearError(): void {
  totpNotice.value = false
  errorMessage.value = ''
}

async function submit(): Promise<void> {
  if (!form.value.email || !form.value.password) {
    showError(t('auth.fillEmailPassword'))
    return
  }
  if (totpRequired.value && !form.value.totp) {
    showError(t('auth.enterTotp'))
    return
  }
  loading.value = true
  clearError()
  try {
    const data = await api.post<AuthResult>('/auth/login', {
      email: form.value.email.trim(),
      password: form.value.password,
      totp: form.value.totp
    })
    await finishLogin(data)
    ElMessage.success(t('auth.loginSuccess'))
  } catch (error: any) {
    if (error.totpRequired) {
      totpRequired.value = true
      showTotpNotice(t('auth.totpRequiredNotice'))
      await nextTick()
      totpInput.value?.focus()
    } else {
      showError(error.message)
    }
  } finally {
    loading.value = false
  }
}

async function submitPasskey(): Promise<void> {
  if (passkeyLoading.value) return
  if (!passkeyUsable.value) {
    showError(t('settings.passkeyUnavailable'))
    return
  }
  passkeyLoading.value = true
  clearError()
  try {
    const data = await loginWithPasskey()
    await finishLogin(data)
    ElMessage.success(t('auth.loginSuccess'))
  } catch (error: any) {
    if (isPasskeyCancelled(error)) {
      showError(t('settings.passkeyCancelled'))
    } else if (error?.status === 401 || error?.status === 400) {
      showError(error?.message || t('auth.passkeyLoginFailed'))
    } else {
      showError(error?.message || t('auth.passkeyLoginFailed'))
    }
  } finally {
    passkeyLoading.value = false
  }
}

// finishLogin 统一处理密码登录与通行密钥登录成功后的跳转与加载层。
async function finishLogin(data: AuthResult): Promise<void> {
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
