#!/usr/bin/env node
// 校验前后端所有语言的 key 与占位符是否一致，并检查前端 t('...') 引用的 key 是否存在。
// 用法: node scripts/check-i18n.ts

import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

type Messages = Record<string, unknown>

interface LocaleData {
  messages: Messages
  keys: Set<string>
  file: string
}

interface EsbuildResult {
  outputFiles: Array<{ text: string }>
}

interface Esbuild {
  build(options: {
    entryPoints: string[]
    bundle: boolean
    format: 'cjs'
    write: false
    platform: 'node'
    logLevel: 'silent'
  }): Promise<EsbuildResult>
}

const scriptDir = dirname(fileURLToPath(import.meta.url))
const root = resolve(scriptDir, '..')
const webDir = join(root, 'web')
const frontendLocalesDir = join(webDir, 'src/i18n/locales')
const backendLocalesDir = join(root, 'internal/i18n/locales')
const defaultLocale = 'zh-CN'

let failed = false

function fail(message: string): void {
  failed = true
  console.error(`✗ ${message}`)
}

function ok(message: string): void {
  console.log(`✓ ${message}`)
}

function isMessages(value: unknown): value is Messages {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function flatten(value: Messages, prefix = '', out = new Set<string>()): Set<string> {
  for (const [key, child] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (isMessages(child)) flatten(child, path, out)
    else out.add(path)
  }
  return out
}

function placeholders(text: unknown): Set<string> {
  return new Set([...String(text).matchAll(/\{([a-zA-Z0-9_]+)\}/g)].map((match) => match[1]))
}

function compare(
  name: string,
  reference: Set<string>,
  referenceName: string,
  candidate: Set<string>,
  candidateName: string
): void {
  const missing = [...reference].filter((key) => !candidate.has(key))
  const extra = [...candidate].filter((key) => !reference.has(key))
  if (missing.length)
    fail(
      `${name}: ${candidateName} 缺少 ${missing.length} 个 key: ${missing.slice(0, 8).join(', ')}${missing.length > 8 ? ' ...' : ''}`
    )
  if (extra.length)
    fail(
      `${name}: ${candidateName} 多出 ${extra.length} 个 key: ${extra.slice(0, 8).join(', ')}${extra.length > 8 ? ' ...' : ''}`
    )
  if (!missing.length && !extra.length) ok(`${name}: ${candidateName} 与 ${referenceName} key 一致`)
}

// ---------------------------- 前端 ----------------------------

const require = createRequire(join(webDir, 'package.json'))
const esbuild: Esbuild = require('esbuild')

async function loadFrontendLocale(file: string): Promise<Messages> {
  const result = await esbuild.build({
    entryPoints: [file],
    bundle: true,
    format: 'cjs',
    write: false,
    platform: 'node',
    logLevel: 'silent'
  })
  const code = result.outputFiles[0].text
  const module: { exports: Messages } = { exports: {} }
  new Function('module', 'exports', 'require', code)(module, module.exports, require)
  return (module.exports.default ?? module.exports) as Messages
}

const frontendFiles = readdirSync(frontendLocalesDir).filter((file) => file.endsWith('.ts'))
const frontend = new Map<string, LocaleData>()
for (const file of frontendFiles) {
  const locale = file.replace(/\.ts$/, '')
  const messages = await loadFrontendLocale(join(frontendLocalesDir, file))
  frontend.set(locale, { messages, keys: flatten(messages), file })
}

if (!frontend.has(defaultLocale)) {
  fail(`前端缺少默认语言 ${defaultLocale}`)
} else {
  const base = frontend.get(defaultLocale)!
  for (const [locale, data] of frontend) {
    if (locale === defaultLocale) continue
    compare('前端', base.keys, defaultLocale, data.keys, locale)

    // 占位符必须完全一致，避免翻译后运行时报错或丢失变量。
    for (const key of [...base.keys].filter((key) => data.keys.has(key))) {
      const expected = placeholders(getValue(base.messages, key))
      const actual = placeholders(getValue(data.messages, key))
      const missing = [...expected].filter((name) => !actual.has(name))
      const extra = [...actual].filter((name) => !expected.has(name))
      if (missing.length || extra.length) {
        fail(
          `前端 ${locale}: ${key} 占位符不一致（缺少 ${missing.join(',') || '-'}，多出 ${extra.join(',') || '-'}）`
        )
      }
    }
  }
}

function getValue(messages: Messages, key: string): unknown {
  return key
    .split('.')
    .reduce<unknown>((value, part) => (isMessages(value) ? value[part] : undefined), messages)
}

// 检查源码中 t('literal') 的 key 是否存在。
const sourceDir = join(webDir, 'src')
function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'i18n') continue
    const path = join(dir, entry.name)
    if (entry.isDirectory()) walk(path, out)
    else if (/\.(vue|ts)$/.test(entry.name)) out.push(path)
  }
  return out
}

if (frontend.has(defaultLocale)) {
  const baseKeys = frontend.get(defaultLocale)!.keys
  const used = new Map<string, string>()
  const pattern = /\bt\(\s*['"]([^'"]+)['"]/g
  for (const file of walk(sourceDir)) {
    const text = readFileSync(file, 'utf8')
    for (const match of text.matchAll(pattern)) {
      const key = match[1]
      if (!used.has(key)) used.set(key, file)
    }
  }
  const missing = [...used].filter(([key]) => !baseKeys.has(key))
  if (missing.length) {
    for (const [key, file] of missing.slice(0, 20)) {
      fail(`前端引用了不存在的 key: ${key} (${file})`)
    }
    if (missing.length > 20) fail(`前端还有 ${missing.length - 20} 个未定义的 key`)
  } else {
    ok(`前端源码引用的 ${used.size} 个 key 均存在`)
  }
}

// ---------------------------- 后端 ----------------------------

const backendFiles = readdirSync(backendLocalesDir).filter((file) => file.endsWith('.json'))
const backend = new Map<string, LocaleData>()
for (const file of backendFiles) {
  const locale = file.replace(/\.json$/, '')
  const messages = JSON.parse(readFileSync(join(backendLocalesDir, file), 'utf8')) as Messages
  backend.set(locale, { messages, keys: new Set(Object.keys(messages)), file })
}

if (!backend.has(defaultLocale)) {
  fail(`后端缺少默认语言 ${defaultLocale}`)
} else {
  const base = backend.get(defaultLocale)!
  for (const [locale, data] of backend) {
    if (locale === defaultLocale) continue
    compare('后端', base.keys, defaultLocale, data.keys, locale)
    for (const key of [...base.keys].filter((key) => data.keys.has(key))) {
      const expected = placeholders(base.messages[key])
      const actual = placeholders(data.messages[key])
      const missing = [...expected].filter((name) => !actual.has(name))
      const extra = [...actual].filter((name) => !expected.has(name))
      if (missing.length || extra.length) {
        fail(
          `后端 ${locale}: ${key} 占位符不一致（缺少 ${missing.join(',') || '-'}，多出 ${extra.join(',') || '-'}）`
        )
      }
    }
  }
}

if (failed) {
  console.error('\n国际化检查未通过。')
  process.exit(1)
}
console.log('\n国际化检查通过。')
