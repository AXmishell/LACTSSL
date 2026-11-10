<template>
  <div class="auth-shell">
    <section class="auth-brand">
      <div style="position: relative; z-index: 1">
        <div class="auth-logo">
          <img src="/logo.svg" alt="LACTSSL" />
          <span>LACTSSL</span>
        </div>
        <h1>{{ t('reset.brandTitle') }}</h1>
        <p>{{ t('reset.brandDesc') }}</p>
      </div>
      <div class="auth-points">
        <div class="auth-point">
          <span class="dot"><el-icon><Lock /></el-icon></span>
          <div><strong>{{ t('reset.passwordReqTitle') }}</strong><br />{{ t('reset.passwordReqDesc') }}</div>
        </div>
        <div class="auth-point">
          <span class="dot"><el-icon><Warning /></el-icon></span>
          <div><strong>{{ t('reset.securityTitle') }}</strong><br />{{ t('reset.securityDesc') }}</div>
        </div>
      </div>
    </section>

    <section class="auth-panel">
      <div class="auth-form">
        <div class="mobile-logo">
          <img src="/logo.svg" alt="LACTSSL" />
          <span>LACTSSL</span>
        </div>
        <h2>{{ t('reset.title') }}</h2>
        <div class="sub">{{ t('reset.subtitle') }}</div>

        <el-alert v-if="valid === false" :title="t('reset.invalid')" type="error" :closable="false" show-icon style="margin-bottom: 20px" />
        <el-alert v-if="done" :title="t('reset.done')" type="success" :closable="false" show-icon style="margin-bottom: 20px" />

        <el-form v-if="valid !== false && !done" :model="form" label-position="top" @submit.prevent="submit">
          <el-form-item :label="t('reset.newPassword')">
            <el-input v-model="form.password" size="large" type="password" show-password :placeholder="t('reset.passwordPlaceholder')">
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item :label="t('reset.confirmNewPassword')">
            <el-input
              v-model="form.confirm"
              size="large"
              type="password"
              show-password
              :placeholder="t('reset.confirmPlaceholder')"
              @keyup.enter="submit"
            >
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-alert v-if="errorMessage" :title="errorMessage" type="error" :closable="false" show-icon style="margin-bottom: 16px" />
          <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">{{ t('reset.confirmReset') }}</el-button>
        </el-form>

        <el-button v-if="valid === false || done" type="primary" size="large" style="width: 100%; margin-top: 8px" @click="goLogin">
          {{ t('reset.backToLogin') }}
        </el-button>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { api, loginPath } from '../api'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const queryToken = route.query.token
const token = ref(typeof queryToken === 'string' ? queryToken : '')
const valid = ref<boolean | null>(null)
const done = ref(false)
const loading = ref(false)
const errorMessage = ref('')
const form = ref({ password: '', confirm: '' })

onMounted(async () => {
  if (!token.value) {
    valid.value = false
    return
  }
  try {
    await api.get('/auth/reset/validate', { token: token.value })
    valid.value = true
  } catch (_) {
    valid.value = false
  }
})

async function submit(): Promise<void> {
  if ((form.value.password || '').length < 8) {
    errorMessage.value = t('reset.passwordMin')
    return
  }
  if (form.value.password !== form.value.confirm) {
    errorMessage.value = t('reset.passwordMismatch')
    return
  }
  loading.value = true
  errorMessage.value = ''
  try {
    await api.post('/auth/reset', { token: token.value, password: form.value.password })
    done.value = true
    ElMessage.success(t('reset.success'))
  } catch (error: any) {
    errorMessage.value = error.message
  } finally {
    loading.value = false
  }
}

function goLogin(): void {
  router.replace(loginPath)
}
</script>
