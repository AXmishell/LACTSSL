#!/usr/bin/env node
// 模拟 Web 插件页“批量启用/停用”操作：
// 使用与前端完全相同的 /api/plugins 读取与 /api/plugins/{providers|deployers|notify-channels} 请求体。
//
// 用法：
//   node scripts/simulate-web-plugin-batch.mjs --base-url http://127.0.0.1:8080 \
//     --setup-token <首次启动令牌> --kind dns --action enable
//   node scripts/simulate-web-plugin-batch.mjs --base-url ... \
//     --email admin@example.com --password password1234 --kind dns --action disable
//
// 也可以传入现成 Cookie/CSRF：
//   --cookie 'lactssl_session=...; lactssl_csrf=...' --csrf <value>

const args = new Map()
for (let i = 2; i < process.argv.length; i++) {
  const key = process.argv[i]
  if (!key.startsWith('--')) continue
  const value = process.argv[i + 1] && !process.argv[i + 1].startsWith('--') ? process.argv[++i] : 'true'
  args.set(key.slice(2), value)
}

const baseURL = (args.get('base-url') || 'http://127.0.0.1:8080').replace(/\/$/, '')
const kind = (args.get('kind') || 'dns').toLowerCase()
const action = (args.get('action') || 'enable').toLowerCase()
const enabled = action !== 'disable'
const setupToken = args.get('setup-token') || ''
const email = args.get('email') || 'admin@example.com'
const password = args.get('password') || 'password1234'

const endpoints = {
  dns: { list: '/api/plugins/providers', path: '/api/plugins', pick: (data) => data.providers || [] },
  deploy: { list: '/api/plugins/deployers', path: '/api/plugins', pick: (data) => data.deploy?.deployers || [] },
  notify: { list: '/api/plugins/notify-channels', path: '/api/plugins', pick: (data) => data.notify?.channels || [] }
}
const endpoint = endpoints[kind]
if (!endpoint) {
  console.error(`unknown kind: ${kind}`)
  process.exit(2)
}

const jar = new Map()
for (const part of (args.get('cookie') || '').split(';')) {
  const [name, ...rest] = part.trim().split('=')
  if (name && rest.length) jar.set(name, rest.join('='))
}
let csrf = args.get('csrf') || jar.get('lactssl_csrf') || ''

function cookieHeader() {
  return [...jar.entries()].map(([name, value]) => `${name}=${value}`).join('; ')
}

function rememberCookies(response) {
  const values = typeof response.headers.getSetCookie === 'function'
    ? response.headers.getSetCookie()
    : (response.headers.get('set-cookie') ? [response.headers.get('set-cookie')] : [])
  for (const line of values) {
    const [pair] = line.split(';')
    const index = pair.indexOf('=')
    if (index <= 0) continue
    const name = pair.slice(0, index).trim()
    const value = pair.slice(index + 1).trim()
    jar.set(name, value)
    if (name === 'lactssl_csrf' || name === '__Host-lactssl_csrf') csrf = value
  }
}

async function request(method, path, body, extraHeaders = {}) {
  const headers = { Accept: 'application/json', ...extraHeaders }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (jar.size) headers.Cookie = cookieHeader()
  if (csrf && method !== 'GET') headers['X-LACTSSL-CSRF'] = csrf
  const response = await fetch(baseURL + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body)
  })
  rememberCookies(response)
  const text = await response.text()
  let json = null
  try { json = text ? JSON.parse(text) : null } catch { /* keep text */ }
  if (!response.ok) {
    throw new Error(`${method} ${path} -> HTTP ${response.status}: ${text}`)
  }
  return json
}

async function login() {
  if (args.get('cookie') || args.get('csrf')) {
    console.log('使用传入的 Cookie/CSRF')
    return
  }
  if (setupToken) {
    console.log('首次初始化管理员…')
    await request('POST', '/api/setup', { email, password }, { 'X-LACTSSL-SETUP-TOKEN': setupToken })
    console.log('初始化完成')
    return
  }
  console.log(`登录 ${email}…`)
  await request('POST', '/api/auth/login', { email, password })
  console.log('登录完成')
}

try {
  await login()

  const status = await request('GET', endpoint.path)
  const items = endpoint.pick(status.data)
  const ids = items.map((item) => item.id).filter(Boolean)
  const initialEnabled = items.filter((item) => item.enabled).length
  console.log(`读取到 ${ids.length} 个 ${kind} 条目，当前已启用 ${initialEnabled}`)
  if (!ids.length) throw new Error('列表为空，无法模拟批量操作')

  // 与 Web 端 bulkProviders 一致的请求体：显式 ids + enabled。
  const result = await request('PUT', endpoint.list, { enabled, ids })
  const data = result.data || {}
  console.log(`批量${enabled ? '启用' : '停用'}响应: updated=${data.updated ?? 0} skipped=${(data.skipped || []).length} errors=${(data.errors || []).length}`)
  if ((data.updated ?? 0) === 0) {
    throw new Error('updated=0，批量操作没有生效')
  }

  const after = await request('GET', endpoint.path)
  const afterItems = endpoint.pick(after.data)
  const enabledCount = afterItems.filter((item) => item.enabled).length
  console.log(`操作后已启用 ${enabledCount} / ${afterItems.length}`)
  if (enabled && enabledCount === 0) {
    throw new Error('批量启用后仍没有启用条目')
  }
  if (!enabled && enabledCount === afterItems.length) {
    throw new Error('批量停用后仍有全部条目处于启用状态')
  }
  console.log('Web 批量操作模拟通过')
} catch (error) {
  console.error(`模拟失败: ${error.message}`)
  process.exit(1)
}
