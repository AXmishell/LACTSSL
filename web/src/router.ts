import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { loginPath } from './api'
import { state, loadUser, loadSettings } from './store'
import { setLocale, t } from './i18n'
import { beginRouteLoading, endRouteLoading } from './loading'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    titleKey?: string
    subtitleKey?: string
    tab?: string
  }
}

const Layout = () => import('./components/Layout.vue')
const NotFound = () => import('./views/NotFound.vue')

// 登录、安装和密码重置使用裸路径；服务端用安全入口门禁 Cookie 保护它们。
// 控制台页面使用裸路径，服务端仅对已登录请求返回 index.html。
const routes: RouteRecordRaw[] = [
  { path: '/login', component: () => import('./views/Login.vue'), meta: { public: true, titleKey: 'nav.login' } },
  { path: '/install', component: () => import('./views/Setup.vue'), meta: { public: true, titleKey: 'setup.title' } },
  { path: '/reset', component: () => import('./views/Reset.vue'), meta: { public: true, titleKey: 'reset.title' } },
  {
    path: '/',
    component: Layout,
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', component: () => import('./views/Dashboard.vue'), meta: { titleKey: 'nav.dashboard', subtitleKey: 'dashboard.expirySub' } },
      { path: 'certificates', component: () => import('./views/Certificates.vue'), meta: { titleKey: 'nav.certificates', subtitleKey: 'certs.subtitle' } },
      { path: 'tasks', component: () => import('./views/Tasks.vue'), meta: { titleKey: 'nav.tasks', subtitleKey: 'tasks.subtitle' } },
      { path: 'monitors', component: () => import('./views/Monitors.vue'), meta: { titleKey: 'nav.monitors', subtitleKey: 'monitors.subtitle' } },
      { path: 'dns', component: () => import('./views/Credentials.vue'), meta: { titleKey: 'nav.dns', subtitleKey: 'credentials.dnsSubtitle', tab: 'dns' } },
      { path: 'ca-configs', component: () => import('./views/CAConfigs.vue'), meta: { titleKey: 'nav.caConfigs', subtitleKey: 'ca.subtitle' } },
      { path: 'private-cas', component: () => import('./views/PrivateCA.vue'), meta: { titleKey: 'nav.privateCA', subtitleKey: 'privateCA.subtitle' } },
      { path: 'acme-server', component: () => import('./views/ACMEServer.vue'), meta: { titleKey: 'nav.acmeServer', subtitleKey: 'acmeServer.subtitle' } },
      { path: 'acme-diagnostics', component: () => import('./views/ACMEDiagnostics.vue'), meta: { titleKey: 'acmeDiagnostics.title', subtitleKey: 'acmeDiagnostics.subtitle' } },
      { path: 'deploy', component: () => import('./views/Credentials.vue'), meta: { titleKey: 'nav.deploy', subtitleKey: 'credentials.deploySubtitle', tab: 'deploy' } },
      { path: 'plugins', component: () => import('./views/Plugins.vue'), meta: { titleKey: 'nav.dnsPlugins', subtitleKey: 'plugins.subtitle' } },
      { path: 'notifications', component: () => import('./views/Notifications.vue'), meta: { titleKey: 'nav.notifications', subtitleKey: 'notifications.subtitle' } },
      { path: 'logs', component: () => import('./views/Logs.vue'), meta: { titleKey: 'nav.logs', subtitleKey: 'logs.subtitle' } },
      { path: 'system-logs', component: () => import('./views/SystemLogs.vue'), meta: { titleKey: 'nav.systemLogs', subtitleKey: 'systemLogs.subtitle' } },
      { path: 'sessions', component: () => import('./views/Sessions.vue'), meta: { titleKey: 'nav.sessions', subtitleKey: 'sessions.subtitle' } },
      { path: 'settings', component: () => import('./views/Settings.vue'), meta: { titleKey: 'nav.settings', subtitleKey: 'settings.subtitle' } },
      // 未匹配的地址保留控制台框架，展示 404 页面而不是静默跳回仪表盘。
      { path: ':pathMatch(.*)*', component: NotFound, meta: { titleKey: 'notFound.title' } }
    ]
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  // 切页回到顶部；浏览器前进/后退时恢复原来的滚动位置。
  scrollBehavior(_to, _from, savedPosition) {
    return savedPosition ?? { top: 0 }
  }
})

// 首次进入由 index.html 的首屏启动层负责，后续切换页面使用全局加载层。
let initialNavigation = true

router.beforeEach(async (to, from) => {
  const isInitial = initialNavigation
  initialNavigation = false
  // 公开页（登录、安装、重置）与公开页之间的切换已有按钮级反馈，
  // 不再叠加整页加载层，避免登录成功后出现多余的等待遮罩。
  if (!isInitial && !from.meta.public && !to.meta.public) beginRouteLoading()

  document.title = to.meta.titleKey ? `${t(to.meta.titleKey)} · LACTSSL` : 'LACTSSL'

  if (to.meta.public) {
    return true
  }

  if (!state.user) {
    try {
      await loadUser()
    } catch (_) {
      if (!isInitial) endRouteLoading()
      return { path: loginPath }
    }
  }

  // 登录后先应用服务端保存的语言，再渲染页面，避免动态元数据使用旧语言。
  if (!state.settings) {
    try {
      const settings = await loadSettings()
      if (settings.language) setLocale(settings.language)
    } catch (_) {
      // 保持本地语言
    }
  }
  return true
})

// 导航成功、被取消或失败都会触发 afterEach，确保加载层不会残留。
router.afterEach(() => {
  endRouteLoading()
})

router.onError(() => {
  endRouteLoading()
})

export default router
