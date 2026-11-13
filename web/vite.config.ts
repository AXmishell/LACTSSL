import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import { readFileSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// 开发服务器需要模拟 Go 服务的安全入口。优先读取环境变量，其次读取配置文件：
//   LACTSSL_ENTRY=xxxxxxxx npm run dev
const RESERVED_ENTRIES = new Set(['api', 'assets', 'healthz', 'static'])

function normalizeEntry(raw: string): string {
  const entry = raw.trim().replace(/^\/+|\/+$/g, '')
  if (!/^[a-zA-Z0-9_-]{8,32}$/.test(entry) || RESERVED_ENTRIES.has(entry.toLowerCase())) {
    return ''
  }
  return entry
}

function resolveEntry(): string {
  const fromEnv = normalizeEntry(process.env.LACTSSL_ENTRY || '')
  if (fromEnv) return fromEnv
  try {
    const file = fileURLToPath(new URL('../config.yaml', import.meta.url))
    const match = readFileSync(file, 'utf8').match(/^\s*entry:\s*["']?([a-zA-Z0-9_-]{8,32})["']?\s*$/m)
    if (match) return normalizeEntry(match[1])
  } catch {
    // 配置文件不存在时忽略，改用环境变量。
  }
  return ''
}

// 与 Go 服务保持一致：仅入口路径、接口和 Vite 自身资源可访问，其余路径直接 404，
// 避免 SPA fallback 把错误入口或 /api 请求吞成 index.html。
function entryGuardPlugin(entry: string): Plugin {
  const prefix = `/${entry}`
  const vitePrefixes = ['/@vite/', '/@fs/', '/@id/', '/@react-refresh', '/node_modules/', '/src/', '/assets/', '/public/', '/__']
  const publicFiles = new Set(['/favicon.svg', '/favicon.ico', '/logo.svg'])

  return {
    name: 'lactssl-entry-guard',
    // 仅开发服务器生效，避免构建产物把安全入口写死进 index.html（生产由 Go 运行时注入）。
    apply: 'serve' as const,
    // 与 Go 服务一致：把当前安全入口注入页面，前端据此拼接口地址。
    transformIndexHtml() {
      if (!entry) return
      const script = `window.__LACTSSL_ENTRY__=${JSON.stringify(entry)};window.__LACTSSL_API_BASE__=${JSON.stringify(`${prefix}/api`)};`
      return [
        {
          tag: 'script',
          children: script,
          injectTo: 'head-prepend' as const
        }
      ]
    },
    configureServer(server) {
      if (!entry) {
        server.config.logger.warn('[lactssl] 未设置 LACTSSL_ENTRY，开发服务器不会校验安全入口')
        return
      }
      server.config.logger.info(`[lactssl] 安全入口: ${prefix}/`)

      // 开发环境用一个固定 Cookie 模拟 Go 的入口门禁；真实会话仍由后端校验。
      const devEntryCookie = 'lactssl_entry=dev'
      const hasEntryCookie = (req: { headers?: Record<string, unknown> }): boolean => {
        const header = String(req.headers?.cookie || '')
        return header.split(';').some((item) => item.trim() === devEntryCookie)
      }
      const setEntryCookie = (res: { setHeader: (name: string, value: string) => void }): void => {
        res.setHeader('Set-Cookie', `${devEntryCookie}; Path=/; HttpOnly; SameSite=Lax; Max-Age=86400`)
      }
      const notFoundPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>404 · LACTSSL</title>
<link rel="stylesheet" href="/src/styles.css?direct">
</head>
<body>
<div id="app">
<div class="shell" style="display:block;min-height:100vh">
  <main class="main-content">
    <div class="page notfound-page" style="min-height:calc(100vh - 72px)">
      <section class="k-card notfound-card">
        <div class="notfound-code" aria-hidden="true">404</div>
        <h2>页面不存在</h2>
        <div class="muted" style="margin-top:4px;font-size:14px">Page not found</div>
        <p class="notfound-desc" style="margin-top:12px">你访问的地址不存在，或安全入口链接已失效。<br>The address you visited does not exist, or the secure entry link has expired.</p>
        <div class="auth-hint">请通过安全入口地址重新打开控制台。<br>Reopen the console through the secure entry URL.</div>
      </section>
    </div>
  </main>
</div>
</div>
</body>
</html>`
      const notFound = (
        res: { statusCode: number; setHeader: (name: string, value: string) => void; end: (body: string) => void },
        isPage = true
      ): void => {
        res.statusCode = 404
        res.setHeader('Cache-Control', 'no-store')
        res.setHeader('Content-Type', isPage ? 'text/html; charset=utf-8' : 'text/plain; charset=utf-8')
        res.end(isPage ? notFoundPage : '404 page not found')
      }

      server.middlewares.use((req, res, next) => {
        const url = req.url || '/'
        const queryIndex = url.indexOf('?')
        const pathname = queryIndex >= 0 ? url.slice(0, queryIndex) : url
        const search = queryIndex >= 0 ? url.slice(queryIndex) : ''

        // ACME 协议与 PKI（CRL/OCSP）由 Go 后端提供，开发时直接透传给代理。
        if (pathname.startsWith('/acme/') || pathname.startsWith('/pki/')) {
          next()
          return
        }

        // 访问安全入口：写入门禁 Cookie，随后切到裸登录页。
        if (pathname === prefix || pathname === `${prefix}/`) {
          setEntryCookie(res)
          res.statusCode = 302
          res.setHeader('Location', `/login${search}`)
          res.end()
          return
        }
        if (pathname.startsWith(`${prefix}/`)) {
          // 入口下的接口继续代理到后端。
          if (pathname.startsWith(`${prefix}/api/`)) {
            next()
            return
          }
          // 兼容旧入口下的页面链接，例如 /<entry>/reset?token=...。
          setEntryCookie(res)
          const rest = pathname.slice(prefix.length)
          const target = ['/login', '/install', '/reset'].includes(rest) ? rest : '/login'
          res.statusCode = 302
          res.setHeader('Location', `${target}${search}`)
          res.end()
          return
        }
        // 错误入口下的接口请求返回 JSON，避免被当成 SPA 页面。
        if (pathname === '/api' || pathname.startsWith('/api/') || pathname.includes('/api/')) {
          res.statusCode = 404
          res.setHeader('Content-Type', 'application/json; charset=utf-8')
          res.end(JSON.stringify({ success: false, message: '接口不存在或安全入口不正确' }))
          return
        }
        // 放行 Vite HMR、源码模块和公共资源。
        if (publicFiles.has(pathname) || vitePrefixes.some((item) => pathname.startsWith(item))) {
          next()
          return
        }
        // 其余页面（/login、/dashboard 等）都需要先拿到入口门禁 Cookie。
        // 开发环境无法校验 Go 的会话，登录态仍由前端路由守卫处理。
        if (/\.[^/]+$/.test(pathname)) {
          notFound(res, false)
          return
        }
        if (!hasEntryCookie(req)) {
          notFound(res)
          return
        }
        if (pathname === '/') {
          res.statusCode = 302
          res.setHeader('Location', '/login')
          res.end()
          return
        }
        next()
      })
    }
  }
}

// 清理构建产物中的注释：Vite 只压缩 JS/CSS，index.html 里的 HTML 注释和
// 内联 <style> 注释会原样保留，而它们最终会通过 go:embed 进入二进制。
function stripCommentsPlugin(): Plugin {
  let outDir = 'dist'
  return {
    name: 'lactssl-strip-comments',
    apply: 'build' as const,
    configResolved(config) {
      outDir = resolve(config.root, config.build.outDir)
    },
    writeBundle() {
      const file = resolve(outDir, 'index.html')
      const html = readFileSync(file, 'utf8')
        // HTML 注释（构建产物中不需要）。
        .replace(/<!--[\s\S]*?-->/g, '')
        // 仅清理 <style> 内的 CSS 注释，避免误伤脚本字符串。
        .replace(/(<style\b[^>]*>)([\s\S]*?)(<\/style>)/gi, (_, open: string, css: string, close: string) => {
          return open + css.replace(/\/\*[\s\S]*?\*\//g, '') + close
        })
      writeFileSync(file, html)
    }
  }
}

const entry = resolveEntry()

// 前端展示的版本号与后端 -ldflags 注入的值同源，避免页面与控制台版本不一致。
function resolveVersion(): string {
  try {
    return readFileSync(fileURLToPath(new URL('../VERSION', import.meta.url)), 'utf8').trim() || 'dev'
  } catch {
    return 'dev'
  }
}

// 生产环境由 Go 服务直接托管 dist，开发时把接口请求代理到后端。
export default defineConfig({
  plugins: [
    vue({
      // 生产构建不保留模板注释，避免注释进入 go:embed 的前端资源。
      template: { compilerOptions: { comments: false } }
    }),
    entryGuardPlugin(entry),
    stripCommentsPlugin()
  ],
  define: {
    __LACTSSL_VERSION__: JSON.stringify(resolveVersion())
  },
  base: '/',
  // 依赖（如 Element Plus）带 /*! ... */ 法律声明注释，esbuild 默认会保留。
  esbuild: {
    legalComments: 'none'
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    minify: 'esbuild',
    cssMinify: 'esbuild',
    sourcemap: false,
    chunkSizeWarningLimit: 1600
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '^/.*?/api/': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true
      },
      // 开发时让浏览器地址（:5173）也能访问 ACME 端点；保留原始 Host，
      // 使后端返回的 directory 地址与浏览器地址一致。
      '^/acme/': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false
      },
      '^/pki/': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false
      }
    }
  }
})
