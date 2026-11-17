<template>
  <div class="page notfound-page">
    <section class="k-card notfound-card">
      <div class="notfound-code" aria-hidden="true">404</div>
      <h2>{{ t('notFound.title') }}</h2>
      <p class="notfound-desc">{{ t('notFound.desc') }}</p>

      <div class="notfound-path">
        <span>{{ t('notFound.path') }}</span>
        <code class="mono break-all">{{ route.fullPath }}</code>
      </div>

      <div class="notfound-actions">
        <el-button type="primary" size="large" @click="router.push('/dashboard')">
          <el-icon><Odometer /></el-icon>{{ t('notFound.backDashboard') }}
        </el-button>
        <el-button size="large" @click="goBack">
          <el-icon><ArrowLeft /></el-icon>{{ t('notFound.goBack') }}
        </el-button>
      </div>

      <div class="notfound-links">
        <span class="muted">{{ t('notFound.quickLinks') }}</span>
        <router-link to="/certificates">{{ t('nav.certificates') }}</router-link>
        <router-link to="/tasks">{{ t('nav.tasks') }}</router-link>
        <router-link to="/logs">{{ t('nav.logs') }}</router-link>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

// 直接打开错误地址时没有可回退的历史，改为回到仪表盘。
function goBack(): void {
  if (window.history.state?.back) router.back()
  else router.push('/dashboard')
}
</script>
