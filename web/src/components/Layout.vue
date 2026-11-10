<template>
  <div class="shell">
    <aside class="sidebar" :class="{ open: mobileOpen }">
      <div class="sidebar-brand">
        <img src="/logo.svg" alt="LACTSSL" />
        <div>
          <div class="brand-name">LACTSSL</div>
          <div class="brand-sub">{{ t('app.brandSub') }}</div>
        </div>
      </div>

      <nav class="sidebar-nav">
        <template v-for="group in menus" :key="group.label">
          <div v-if="group.label" class="nav-group">{{ t(group.label) }}</div>
          <div
            v-for="item in group.items"
            :key="item.path"
            class="nav-item"
            :class="{ active: isActive(item.path) }"
            @click="go(item.path)"
          >
            <el-icon><component :is="item.icon" /></el-icon>
            <span>{{ t(item.label) }}</span>
          </div>
        </template>
      </nav>

      <div class="sidebar-foot">
        <el-dropdown trigger="click" placement="top-start" @command="handleUserCommand">
          <div class="user-chip">
            <div class="avatar">{{ initial }}</div>
            <div class="user-meta">
              <div class="user-mail">{{ user?.email || t('nav.userNotLoggedIn') }}</div>
              <div class="user-role">{{ t('nav.admin') }} · {{ t('nav.personal') }}</div>
            </div>
            <el-icon style="color: #94a3b8"><ArrowUp /></el-icon>
          </div>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="settings">
                <el-icon><Setting /></el-icon>{{ t('nav.settings') }}
              </el-dropdown-item>
              <el-dropdown-item command="logout" divided>
                <el-icon><SwitchButton /></el-icon>{{ t('nav.logout') }}
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </aside>

    <div class="overlay" :class="{ show: mobileOpen }" @click="mobileOpen = false"></div>

    <div class="main-wrap">
      <header class="topbar">
        <div class="topbar-left">
          <el-button class="mobile-toggle" text circle @click="mobileOpen = !mobileOpen">
            <el-icon size="20"><Menu /></el-icon>
          </el-button>
          <div class="crumb">
            {{ t('nav.console') }} <span style="margin: 0 6px; color: #cbd5e1">/</span>
            <strong>{{ route.meta.titleKey ? t(route.meta.titleKey) : t('nav.dashboard') }}</strong>
          </div>
        </div>
        <div class="topbar-right">
          <el-dropdown trigger="click" @command="changeLocale">
            <el-button text size="small">
              <el-icon style="margin-right: 4px"><Connection /></el-icon>{{ currentLanguageLabel }}
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-for="locale in locales" :key="locale" :command="locale" :disabled="locale === activeLocale">
                  {{ localeMeta[locale]?.label || locale }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <span class="tag-soft tag-green">
            <span class="list-dot" style="background: #10b981; width: 6px; height: 6px"></span>
            {{ t('nav.serviceRunning') }}
          </span>
          <el-tooltip :content="t('nav.refresh')" placement="bottom">
            <el-button text circle @click="refresh">
              <el-icon size="17"><Refresh /></el-icon>
            </el-button>
          </el-tooltip>
        </div>
      </header>

      <main class="main-content">
        <router-view v-slot="{ Component }">
          <component :is="Component" :key="route.fullPath" />
        </router-view>
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessageBox } from 'element-plus'
import { api, loginPath } from '../api'
import { state, logout } from '../store'
import { availableLocales, currentLocale, localeMeta, setLocale } from '../i18n'
import DnsAccountIcon from './DnsAccountIcon.vue'
import CAConfigIcon from './CAConfigIcon.vue'

interface MenuItem {
  path: string
  label: string
  icon: string | Component
}

interface MenuGroup {
  label: string
  items: MenuItem[]
}

const route = useRoute()
const router = useRouter()
const { t, locale } = useI18n()
const mobileOpen = ref(false)

const user = computed(() => state.user)
const initial = computed(() => (state.user?.email || 'K').slice(0, 1).toUpperCase())
const activeLocale = computed(() => locale.value || currentLocale())
const locales = computed(() => availableLocales())
const currentLanguageLabel = computed(() => localeMeta[activeLocale.value]?.label || activeLocale.value)

const menus: MenuGroup[] = [
  {
    label: '',
    items: [{ path: '/dashboard', label: 'nav.dashboard', icon: 'Odometer' }]
  },
  {
    label: 'nav.groupCertificates',
    items: [
      { path: '/certificates', label: 'nav.certificates', icon: 'Document' },
      { path: '/tasks', label: 'nav.tasks', icon: 'Promotion' },
      { path: '/monitors', label: 'nav.monitors', icon: 'Monitor' },
      { path: '/dns', label: 'nav.dns', icon: DnsAccountIcon },
      { path: '/ca-configs', label: 'nav.caConfigs', icon: CAConfigIcon },
      { path: '/private-cas', label: 'nav.privateCA', icon: 'Lock' },
      { path: '/acme-server', label: 'nav.acmeServer', icon: 'Connection' },
      { path: '/acme-diagnostics', label: 'nav.acmeDiagnostics', icon: 'DataAnalysis' },
      { path: '/deploy', label: 'nav.deploy', icon: 'UploadFilled' }
    ]
  },
  {
    label: 'nav.groupSystem',
    items: [
      { path: '/notifications', label: 'nav.notifications', icon: 'Bell' },
      { path: '/logs', label: 'nav.logs', icon: 'Tickets' },
      { path: '/sessions', label: 'nav.sessions', icon: 'Key' },
      { path: '/settings', label: 'nav.settings', icon: 'Setting' }
    ]
  }
]

watch(
  () => route.fullPath,
  () => {
    mobileOpen.value = false
  }
)


function isActive(path: string): boolean {
  return route.path === path
}

function go(path: string): void {
  if (route.path !== path) router.push(path)
}

function refresh(): void {
  window.location.reload()
}

async function changeLocale(value: string): Promise<void> {
  setLocale(value)
  try {
    await api.put('/settings', { language: value })
  } catch (_) {
    // 语言已本地生效，保存失败不影响使用
  }
  // 重新加载页面，让后端返回的元数据、日志与状态全部切换到新语言。
  window.location.reload()
}

async function handleUserCommand(command: string): Promise<void> {
  if (command === 'settings') {
    router.push('/settings')
    return
  }
  if (command === 'logout') {
    try {
      await ElMessageBox.confirm(t('nav.logoutConfirm'), t('nav.logoutTitle'), {
        confirmButtonText: t('nav.exit'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      })
      // 通知服务端清除 HttpOnly 会话 Cookie，失败也要清理本地状态。
      try {
        await api.post('/auth/logout')
      } catch (_) {
        // 忽略
      }
      logout()
      router.push(loginPath)
    } catch (_) {
      // 用户取消
    }
  }
}
</script>
