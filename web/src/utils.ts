// 前端通用格式化与状态映射工具。

import type { Certificate } from './types'
import { currentLocale, t } from './i18n'

export type TagType = 'success' | 'info' | 'warning' | 'danger'

export interface StateInfo {
  label: string
  type: TagType
}

export function parseDate(value?: string | null): Date | null {
  if (!value) return null
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

export function formatDate(value?: string | null, withTime = true): string {
  const date = parseDate(value)
  if (!date) return t('common.none')
  const options: Intl.DateTimeFormatOptions = {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  }
  if (withTime) {
    options.hour = '2-digit'
    options.minute = '2-digit'
  }
  return new Intl.DateTimeFormat(currentLocale(), options).format(date)
}

export function fromNow(value?: string | null): string {
  const date = parseDate(value)
  if (!date) return t('common.none')
  const diff = date.getTime() - Date.now()
  const abs = Math.abs(diff)
  if (abs < 60 * 1000) return t('status.justNow')
  const formatter = new Intl.RelativeTimeFormat(currentLocale(), { numeric: 'auto' })
  if (abs < 60 * 60 * 1000) return formatter.format(Math.round(diff / 60000), 'minute')
  if (abs < 24 * 60 * 60 * 1000) return formatter.format(Math.round(diff / 3600000), 'hour')
  if (abs < 30 * 24 * 60 * 60 * 1000) return formatter.format(Math.round(diff / 86400000), 'day')
  return formatDate(value, false)
}

export function daysLeft(value?: string | null): number {
  const date = parseDate(value)
  if (!date) return 0
  return Math.ceil((date.getTime() - Date.now()) / 86400000)
}

export function isExpired(value?: string | null): boolean {
  const date = parseDate(value)
  return date !== null && date.getTime() < Date.now()
}

export function certState(cert?: Pick<Certificate, 'not_after' | 'status'> | null): StateInfo {
  if (!cert) return { label: t('status.unknown'), type: 'info' }
  if (cert.status === 'revoked') return { label: t('status.revoked'), type: 'danger' }
  if (isExpired(cert.not_after)) return { label: t('status.expired'), type: 'danger' }
  const left = daysLeft(cert.not_after)
  if (left <= 30) return { label: t('status.daysLeft', { days: left }), type: left <= 7 ? 'danger' : 'warning' }
  return { label: t('status.valid'), type: 'success' }
}

export function taskState(status?: string | null): StateInfo {
  const map: Record<string, StateInfo> = {
    pending: { label: t('status.pendingTask'), type: 'info' },
    running: { label: t('status.running'), type: 'warning' },
    success: { label: t('status.success'), type: 'success' },
    partial: { label: t('status.partial'), type: 'warning' },
    failed: { label: t('status.failed'), type: 'danger' }
  }
  return (status && map[status]) || { label: status || t('status.unknown'), type: 'info' }
}

export function monitorState(status?: string | null): StateInfo {
  const map: Record<string, StateInfo> = {
    valid: { label: t('status.valid'), type: 'success' },
    expiring: { label: t('status.expiring'), type: 'warning' },
    expired: { label: t('status.expired'), type: 'danger' },
    error: { label: t('status.error'), type: 'danger' },
    pending: { label: t('status.pending'), type: 'info' }
  }
  return (status && map[status]) || { label: status || t('status.unknown'), type: 'info' }
}

export function levelType(level?: string | null): TagType {
  const map: Record<string, TagType> = { success: 'success', info: 'info', warning: 'warning', error: 'danger' }
  return (level && map[level]) || 'info'
}

export function providerLabel(value: string, groups?: { value: string; label: string }[][]): string {
  for (const group of groups || []) {
    for (const item of group) {
      if (item.value === value) return item.label
    }
  }
  return value
}

export function splitLines(text?: string | null): string[] {
  return String(text || '')
    .split(/[\n,;]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

export function shortDomains(domains?: string[] | null, max = 2): string {
  if (!domains || !domains.length) return t('common.none')
  if (domains.length <= max) return domains.join(', ')
  return `${domains.slice(0, max).join(', ')} ${t('status.etc', { count: domains.length - max })}`
}
