import type { Settings } from './types'

export interface NotifyChannelMeta {
  value: string
  labelKey?: string
  label?: string
}

// 官方通知渠道的本地化 key。插件返回的 name 只作为第三方渠道的兜底展示。
const channelLabelKeys: Record<string, string> = {
  mail: 'notifications.channelMail',
  gotify: 'notifications.channelGotify',
  ntfy: 'notifications.channelNtfy',
  gocqhttp: 'notifications.channelGocqhttp',
  serverchan: 'notifications.channelServerChan',
  pushdeer: 'notifications.channelPushdeer',
  bark: 'notifications.channelBark',
  telegram: 'notifications.channelTelegram',
  dingtalk: 'notifications.channelDingtalk',
  wecom_bot: 'notifications.channelWecomBot',
  wecom_app: 'notifications.channelWecomApp',
  wecom_smart: 'notifications.channelWecomSmart',
  igot: 'notifications.channelIgot',
  pushplus: 'notifications.channelPushplus',
  weplus: 'notifications.channelWeplus',
  wxpusher: 'notifications.channelWxpusher',
  wxpusher_spt: 'notifications.channelWxpusherSpt',
  openilink: 'notifications.channelOpenilink',
  wpush: 'notifications.channelWpush',
  synology_chat: 'notifications.channelSynologyChat',
  feishu: 'notifications.channelFeishu',
  pushme: 'notifications.channelPushme',
  chronocat: 'notifications.channelChronocat',
  custom: 'notifications.channelCustom'
}

export function notifyChannelLabelKey(id: string): string | undefined {
  return channelLabelKeys[id]
}

export function notifyChannelMeta(settings: Settings | null | undefined): NotifyChannelMeta[] {
  const defs = settings?.notify_channel_defs || []
  return defs.map((def) => ({
    value: def.id,
    labelKey: channelLabelKeys[def.id],
    label: def.name || def.id
  }))
}

export function notifyChannelEnabled(settings: Settings | null | undefined, value: string): boolean {
  if (!settings) return false
  if (value === 'mail') return !!settings.notify_enabled
  return settings.notify_channels?.[value]?.enabled === true
}

export function enabledNotifyChannels(settings: Settings | null | undefined): NotifyChannelMeta[] {
  if (!settings) return []
  const channels: NotifyChannelMeta[] = []
  if (settings.notify_enabled) {
    channels.push({ value: 'mail', labelKey: channelLabelKeys.mail })
  }
  for (const item of notifyChannelMeta(settings)) {
    if (notifyChannelEnabled(settings, item.value)) channels.push(item)
  }
  return channels
}
