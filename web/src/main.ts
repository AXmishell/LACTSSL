import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
import 'element-plus/dist/index.css'
import './styles.css'
import App from './App.vue'
import router from './router'
import { i18n, currentLocale, elementLocaleFor } from './i18n'
import { installConsoleEasterEgg } from './consoleEasterEgg'

const bootedAt = performance.now()
const BOOT_MIN_VISIBLE = 240

const app = createApp(App)
for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}
app.use(ElementPlus, { locale: elementLocaleFor(currentLocale()) })
app.use(i18n)
app.use(router)
app.mount('#app')

// 控制台彩蛋：品牌横幅、安全提醒与 lactssl 小工具。
installConsoleEasterEgg()

// 首屏路由与登录态检查完成后再淡出启动层，避免白屏或页面未就绪时闪现。
router
  .isReady()
  .catch(() => undefined)
  .then(() => {
    const wait = Math.max(0, BOOT_MIN_VISIBLE - (performance.now() - bootedAt))
    window.setTimeout(() => {
      const boot = document.getElementById('app-boot')
      if (!boot) return
      boot.classList.add('is-hiding')
      window.setTimeout(() => boot.remove(), 260)
    }, wait)
  })
