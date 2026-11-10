import type { Settings } from './types'

export interface NotifyChannelMeta {
  value: string
  labelKey: string
}

// 通知渠道顺序与「通知」页面保持一致，供任务通知模块选择已启用渠道。
export const notifyChannelMeta: NotifyChannelMeta[] = [
  { value: 'mail', labelKey: 'notifications.channelMail' },
  { value: 'gotify', labelKey: 'notifications.channelGotify' },
  { value: 'ntfy', labelKey: 'notifications.channelNtfy' },
  { value: 'gocqhttp', labelKey: 'notifications.channelGocqhttp' },
  { value: 'serverchan', labelKey: 'notifications.channelServerChan' },
  { value: 'pushdeer', labelKey: 'notifications.channelPushdeer' },
  { value: 'bark', labelKey: 'notifications.channelBark' },
  { value: 'telegram', labelKey: 'notifications.channelTelegram' },
  { value: 'dingtalk', labelKey: 'notifications.channelDingtalk' },
  { value: 'wecom_bot', labelKey: 'notifications.channelWecomBot' },
  { value: 'wecom_app', labelKey: 'notifications.channelWecomApp' },
  { value: 'wecom_smart', labelKey: 'notifications.channelWecomSmart' },
  { value: 'igot', labelKey: 'notifications.channelIgot' },
  { value: 'pushplus', labelKey: 'notifications.channelPushplus' },
  { value: 'weplus', labelKey: 'notifications.channelWeplus' },
  { value: 'wxpusher', labelKey: 'notifications.channelWxpusher' },
  { value: 'wxpusher_spt', labelKey: 'notifications.channelWxpusherSpt' },
  { value: 'openilink', labelKey: 'notifications.channelOpenilink' },
  { value: 'wpush', labelKey: 'notifications.channelWpush' },
  { value: 'synology_chat', labelKey: 'notifications.channelSynologyChat' },
  { value: 'feishu', labelKey: 'notifications.channelFeishu' },
  { value: 'pushme', labelKey: 'notifications.channelPushme' },
  { value: 'chronocat', labelKey: 'notifications.channelChronocat' },
  { value: 'custom', labelKey: 'notifications.channelCustom' }
]

export function notifyChannelEnabled(settings: Settings | null | undefined, value: string): boolean {
  if (!settings) return false
  if (value === 'mail') return !!settings.notify_enabled
  if (value === 'gotify') return !!settings.gotify_enabled
  if (value === 'serverchan') return !!settings.serverchan_enabled
  return settings.notify_channels?.[value]?.enabled === true
}

export function enabledNotifyChannels(settings: Settings | null | undefined): NotifyChannelMeta[] {
  return notifyChannelMeta.filter((item) => notifyChannelEnabled(settings, item.value))
}
