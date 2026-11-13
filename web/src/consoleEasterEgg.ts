import { t } from './i18n'

// 构建时由 Vite 注入，见 web/vite.config.ts 与 env.d.ts。
declare const __LACTSSL_VERSION__: string

const VERSION = typeof __LACTSSL_VERSION__ === 'string' ? __LACTSSL_VERSION__ : 'dev'

const BANNER = [
  ' ██╗      █████╗  ██████╗████████╗███████╗███████╗██╗',
  ' ██║     ██╔══██╗██╔════╝╚══██╔══╝██╔════╝██╔════╝██║',
  ' ██║     ███████║██║        ██║   ███████╗███████╗██║',
  ' ██║     ██╔══██║██║        ██║   ╚════██║╚════██║██║',
  ' ███████╗██║  ██║╚██████╗   ██║   ███████║███████║███████╗',
  ' ╚══════╝╚═╝  ╚═╝ ╚═════╝   ╚═╝   ╚══════╝╚══════╝╚══════╝'
]

const BANNER_COLORS = ['#93c5fd', '#60a5fa', '#3b82f6', '#2563eb', '#1d4ed8', '#1e40af']

const MONO = 'font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace'

// 每行一个 %c，用蓝色渐变把品牌名画进控制台。
function paintBanner(): void {
  console.log(
    BANNER.map((line) => `%c${line}`).join('\n'),
    ...BANNER_COLORS.map((color) => `color:${color};${MONO};font-size:11px;font-weight:700;line-height:1.15`)
  )
}

function paintIntro(): void {
  console.log(
    `%c LACTSSL %c v${VERSION} %c ${t('console.tagline')} `,
    'background:#2563eb;color:#fff;border-radius:4px 0 0 4px;padding:3px 8px;font-weight:700;font-size:12px',
    'background:#0f172a;color:#a7f3d0;padding:3px 8px;font-weight:600;font-size:12px',
    'color:#64748b;font-size:12px;padding:3px 0'
  )

  // 一行低调的引导，真正的暗号留给好奇的人。
  console.log(
    `%c${t('console.hint')}`,
    'color:#94a3b8;font-size:12px;font-style:italic'
  )
}

function showHelp(): void {
  console.log(
    `%c${t('console.tagline')}`,
    'color:#2563eb;font-size:13px;font-weight:600'
  )
  console.table([
    { command: 'lactssl.about()', description: t('console.cmdAbout') },
    { command: 'lactssl.version', description: t('console.cmdVersion') }
  ])
  console.log(`%c${t('console.cmdClose')}`, 'color:#94a3b8;font-size:12px;font-style:italic')
}

interface LactsslConsoleApi {
  /** 当前前端构建版本。 */
  readonly version: string
  /** 重新打印启动横幅。 */
  about: () => void
  /** 列出控制台里可用的彩蛋命令。 */
  help: () => void
}

let installed = false

export function installConsoleEasterEgg(): void {
  if (installed) return
  installed = true

  paintBanner()
  paintIntro()

  // 暴露一个只读小工具，方便在控制台里反复触发彩蛋。
  const api: LactsslConsoleApi = Object.freeze({
    version: VERSION,
    about: paintBanner,
    help: showHelp
  })
  Object.defineProperty(window, 'lactssl', {
    value: api,
    enumerable: false,
    configurable: false,
    writable: false
  })
}
