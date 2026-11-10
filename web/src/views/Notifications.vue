<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('notifications.title') }}</h1>
        <p>{{ t('notifications.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle @click="reload" />
      </div>
    </div>

    <div class="filter-row">
      <el-radio-group v-model="channel" size="default">
        <el-radio-button v-for="item in channelDefs" :key="item.value" :value="item.value">{{ t(item.labelKey) }}</el-radio-button>
      </el-radio-group>
      <span class="muted">{{ t('notifications.channelHint') }}</span>
    </div>

    <div v-if="channel === 'mail'" class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ t('notifications.smtpTitle') }}</h3>
          <div class="sub">{{ t('notifications.smtpSub') }}</div>
        </div>
      </div>
      <el-form :model="mail" label-position="top">
        <div class="grid grid-3" style="gap: 0 18px">
          <el-form-item :label="t('notifications.smtpHost')">
            <el-input v-model="mail.smtp_host" placeholder="smtp.example.com" />
          </el-form-item>
          <el-form-item :label="t('notifications.port')">
            <el-input-number v-model="mail.smtp_port" :min="1" :max="65535" style="width: 100%" />
          </el-form-item>
          <el-form-item :label="t('notifications.encryption')">
            <el-select v-model="mail.smtp_ssl" style="width: 100%">
              <el-option :value="true" :label="t('notifications.ssl')" />
              <el-option :value="false" :label="t('notifications.starttls')" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('notifications.username')">
            <el-input v-model="mail.smtp_username" :placeholder="t('notifications.usernamePlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('notifications.password')">
            <el-input
              v-model="mail.smtp_password"
              type="password"
              show-password
              :placeholder="settings?.smtp_password_set ? t('notifications.passwordPlaceholderSet') : t('notifications.passwordPlaceholderNew')"
            />
          </el-form-item>
          <el-form-item :label="t('notifications.from')">
            <el-input v-model="mail.smtp_from" :placeholder="t('notifications.fromPlaceholder')" />
          </el-form-item>
        </div>

        <el-divider />

        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('notifications.channelEnabled')">
            <div class="flex gap-10" style="height: 32px">
              <el-switch v-model="mail.notify_enabled" />
            </div>
          </el-form-item>
          <el-form-item :label="t('notifications.notifyTo')">
            <el-input v-model="mail.notify_to" :placeholder="t('notifications.notifyToPlaceholder')" />
          </el-form-item>
        </div>

        <div class="flex gap-10 wrap">
          <el-button type="primary" :loading="savingMail" @click="saveMail">{{ t('notifications.saveMail') }}</el-button>
          <el-button :loading="testing" @click="sendTest">{{ t('notifications.sendTest') }}</el-button>
          <span class="muted" style="align-self: center; font-size: 12px">{{ t('notifications.testHint') }}</span>
        </div>
      </el-form>
    </div>

    <div v-if="channel === 'gotify'" class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ t('notifications.gotifyTitle') }}</h3>
          <div class="sub">{{ t('notifications.gotifySub') }}</div>
        </div>
      </div>
      <el-form :model="gotifyForm" label-position="top">
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('notifications.gotifyURL')">
            <el-input v-model="gotifyForm.url" placeholder="https://gotify.example.com" />
          </el-form-item>
          <el-form-item :label="t('notifications.gotifyToken')">
            <el-input
              v-model="gotifyForm.token"
              type="password"
              show-password
              :placeholder="settings?.gotify_token_set ? t('notifications.gotifyTokenPlaceholderSet') : t('notifications.gotifyTokenPlaceholderNew')"
            />
          </el-form-item>
          <el-form-item :label="t('notifications.gotifyPriority')">
            <el-input-number v-model="gotifyForm.priority" :min="0" :max="10" style="width: 100%" />
          </el-form-item>
          <el-form-item :label="t('notifications.gotifyEnabled')">
            <div class="flex gap-10" style="height: 32px">
              <el-switch v-model="gotifyForm.enabled" />
            </div>
          </el-form-item>
        </div>

        <div class="flex gap-10 wrap">
          <el-button type="primary" :loading="savingGotify" @click="saveGotify">{{ t('notifications.saveGotify') }}</el-button>
          <el-button :loading="testingGotify" @click="sendGotifyTest">{{ t('notifications.sendGotifyTest') }}</el-button>
          <span class="muted" style="align-self: center; font-size: 12px">{{ t('notifications.gotifyTestHint') }}</span>
        </div>
      </el-form>
    </div>

    <div v-if="channel === 'serverchan'" class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ t('notifications.serverchanTitle') }}</h3>
          <div class="sub">{{ t('notifications.serverchanSub') }}</div>
        </div>
      </div>
      <el-form :model="serverChanForm" label-position="top">
        <div class="grid grid-3" style="gap: 0 18px">
          <el-form-item :label="t('notifications.serverchanVersion')">
            <el-select v-model="serverChanForm.version" style="width: 100%">
              <el-option value="auto" :label="t('notifications.serverchanVersionAuto')" />
              <el-option value="turbo" :label="t('notifications.serverchanVersionTurbo')" />
              <el-option value="sc3" :label="t('notifications.serverchanVersionSC3')" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('notifications.serverchanSendKey')">
            <el-input
              v-model="serverChanForm.sendkey"
              type="password"
              show-password
              :placeholder="settings?.serverchan_sendkey_set ? t('notifications.serverchanSendKeyPlaceholderSet') : t('notifications.serverchanSendKeyPlaceholderNew')"
            />
          </el-form-item>
          <el-form-item :label="t('notifications.serverchanEnabled')">
            <div class="flex gap-10" style="height: 32px">
              <el-switch v-model="serverChanForm.enabled" />
            </div>
          </el-form-item>
        </div>
        <div class="form-help">{{ t('notifications.serverchanVersionHint') }}</div>

        <div class="flex gap-10 wrap">
          <el-button type="primary" :loading="savingServerChan" @click="saveServerChan">{{ t('notifications.saveServerChan') }}</el-button>
          <el-button :loading="testingServerChan" @click="sendServerChanTest">{{ t('notifications.sendServerChanTest') }}</el-button>
          <span class="muted" style="align-self: center; font-size: 12px">{{ t('notifications.serverchanTestHint') }}</span>
        </div>
      </el-form>
    </div>

    <div v-if="currentDef" class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ t(currentDef.labelKey) }}</h3>
          <div class="sub">{{ t('notifications.genericSub', { name: t(currentDef.labelKey) }) }}</div>
        </div>
      </div>
      <el-form :model="currentForm" label-position="top">
        <div class="grid grid-3" style="gap: 0 18px">
          <el-form-item
            v-for="field in currentDef.fields"
            :key="field.key"
            :label="t(field.labelKey)"
            :style="field.full ? 'grid-column: 1 / -1' : undefined"
          >
            <el-input
              v-if="!field.type || field.type === 'text'"
              v-model="currentForm[field.key]"
              :placeholder="field.placeholder"
            />
            <el-input
              v-else-if="field.type === 'password'"
              v-model="currentForm[field.key]"
              type="password"
              show-password
              :placeholder="field.placeholder || t('notifications.secretPlaceholder')"
            />
            <el-input-number
              v-else-if="field.type === 'number'"
              v-model="currentForm[field.key]"
              :min="field.min"
              :max="field.max"
              style="width: 100%"
            />
            <el-select v-else-if="field.type === 'select'" v-model="currentForm[field.key]" style="width: 100%">
              <el-option v-for="option in field.options" :key="option.value" :value="option.value" :label="t(option.labelKey)" />
            </el-select>
            <el-input
              v-else-if="field.type === 'textarea'"
              v-model="currentForm[field.key]"
              type="textarea"
              :rows="3"
              :placeholder="field.placeholder"
            />
            <el-switch v-else-if="field.type === 'switch'" v-model="currentForm[field.key]" />
          </el-form-item>
          <el-form-item :label="t('notifications.channelEnabled')">
            <div class="flex gap-10" style="height: 32px">
              <el-switch v-model="currentForm.enabled" />
            </div>
          </el-form-item>
        </div>

        <div class="flex gap-10 wrap">
          <el-button type="primary" :loading="savingGeneric" @click="saveGeneric">{{ t('notifications.saveChannel') }}</el-button>
          <el-button :loading="testingGeneric" @click="testGeneric">{{ t('notifications.testChannel') }}</el-button>
          <span class="muted" style="align-self: center; font-size: 12px">{{ t('notifications.channelTestHint') }}</span>
        </div>
      </el-form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { loadSettings } from '../store'
import type { MessageResult, Settings as AppSettings } from '../types'

interface MailForm {
  smtp_host: string
  smtp_port: number
  smtp_username: string
  smtp_password: string
  smtp_from: string
  smtp_ssl: boolean
  notify_enabled: boolean
  notify_to: string
}

interface GotifyForm {
  url: string
  token: string
  priority: number
  enabled: boolean
}

interface ServerChanForm {
  sendkey: string
  version: 'auto' | 'turbo' | 'sc3'
  enabled: boolean
}

interface ChannelField {
  key: string
  labelKey: string
  type?: 'text' | 'password' | 'number' | 'select' | 'textarea' | 'switch'
  placeholder?: string
  options?: { value: string; labelKey: string }[]
  min?: number
  max?: number
  default?: string | number | boolean
  full?: boolean
}

interface ChannelDef {
  value: string
  labelKey: string
  fields?: ChannelField[]
}

const text = (key: string, labelKey: string, placeholder?: string): ChannelField => ({ key, labelKey, placeholder })
const secret = (key: string, labelKey: string): ChannelField => ({ key, labelKey, type: 'password' })

const channelDefs: ChannelDef[] = [
  { value: 'mail', labelKey: 'notifications.channelMail' },
  { value: 'gotify', labelKey: 'notifications.channelGotify' },
  {
    value: 'ntfy',
    labelKey: 'notifications.channelNtfy',
    fields: [
      { key: 'url', labelKey: 'notifications.fieldServerUrl', default: 'https://ntfy.sh' },
      text('topic', 'notifications.fieldTopic', 'my-topic'),
      secret('token', 'notifications.fieldToken'),
      { key: 'priority', labelKey: 'notifications.fieldPriority', type: 'number', default: 3, min: 1, max: 5 }
    ]
  },
  {
    value: 'gocqhttp',
    labelKey: 'notifications.channelGocqhttp',
    fields: [
      { key: 'url', labelKey: 'notifications.fieldServerUrl', default: 'http://127.0.0.1:5700' },
      secret('access_token', 'notifications.fieldAccessToken'),
      {
        key: 'target_type',
        labelKey: 'notifications.fieldTargetType',
        type: 'select',
        default: 'private',
        options: [
          { value: 'private', labelKey: 'notifications.optionPrivate' },
          { value: 'group', labelKey: 'notifications.optionGroup' }
        ]
      },
      text('target_id', 'notifications.fieldTargetId', 'QQ 号或群号')
    ]
  },
  { value: 'serverchan', labelKey: 'notifications.channelServerChan' },
  {
    value: 'pushdeer',
    labelKey: 'notifications.channelPushdeer',
    fields: [
      { key: 'url', labelKey: 'notifications.fieldServerUrl', default: 'https://api2.pushdeer.com' },
      secret('pushkey', 'notifications.fieldPushKey')
    ]
  },
  {
    value: 'bark',
    labelKey: 'notifications.channelBark',
    fields: [
      { key: 'url', labelKey: 'notifications.fieldServerUrl', default: 'https://api.day.app' },
      secret('key', 'notifications.fieldKey'),
      text('sound', 'notifications.fieldSound'),
      text('group', 'notifications.fieldGroup')
    ]
  },
  {
    value: 'telegram',
    labelKey: 'notifications.channelTelegram',
    fields: [secret('bot_token', 'notifications.fieldBotToken'), text('chat_id', 'notifications.fieldChatId')]
  },
  {
    value: 'dingtalk',
    labelKey: 'notifications.channelDingtalk',
    fields: [secret('webhook', 'notifications.fieldWebhook'), secret('secret', 'notifications.fieldSecret')]
  },
  {
    value: 'wecom_bot',
    labelKey: 'notifications.channelWecomBot',
    fields: [secret('webhook', 'notifications.fieldWebhook')]
  },
  {
    value: 'wecom_app',
    labelKey: 'notifications.channelWecomApp',
    fields: [
      text('corp_id', 'notifications.fieldCorpId'),
      secret('corp_secret', 'notifications.fieldCorpSecret'),
      { key: 'agent_id', labelKey: 'notifications.fieldAgentId', type: 'number', default: 0 },
      text('to_user', 'notifications.fieldToUser', '@all')
    ]
  },
  {
    value: 'wecom_smart',
    labelKey: 'notifications.channelWecomSmart',
    fields: [text('url', 'notifications.fieldUrl'), secret('token', 'notifications.fieldToken')]
  },
  { value: 'igot', labelKey: 'notifications.channelIgot', fields: [secret('key', 'notifications.fieldKey')] },
  {
    value: 'pushplus',
    labelKey: 'notifications.channelPushplus',
    fields: [secret('token', 'notifications.fieldToken'), text('topic', 'notifications.fieldTopic')]
  },
  {
    value: 'weplus',
    labelKey: 'notifications.channelWeplus',
    fields: [text('url', 'notifications.fieldUrl'), secret('token', 'notifications.fieldToken')]
  },
  {
    value: 'wxpusher',
    labelKey: 'notifications.channelWxpusher',
    fields: [secret('app_token', 'notifications.fieldAppToken'), text('uids', 'notifications.fieldUids'), text('topic_ids', 'notifications.fieldTopicIds')]
  },
  {
    value: 'wxpusher_spt',
    labelKey: 'notifications.channelWxpusherSpt',
    fields: [secret('spt', 'notifications.fieldSpt')]
  },
  {
    value: 'openilink',
    labelKey: 'notifications.channelOpenilink',
    fields: [text('url', 'notifications.fieldUrl'), secret('token', 'notifications.fieldToken')]
  },
  {
    value: 'wpush',
    labelKey: 'notifications.channelWpush',
    fields: [text('url', 'notifications.fieldUrl'), secret('token', 'notifications.fieldToken')]
  },
  {
    value: 'synology_chat',
    labelKey: 'notifications.channelSynologyChat',
    fields: [secret('url', 'notifications.fieldWebhook')]
  },
  {
    value: 'feishu',
    labelKey: 'notifications.channelFeishu',
    fields: [secret('webhook', 'notifications.fieldWebhook'), secret('secret', 'notifications.fieldSecret')]
  },
  {
    value: 'pushme',
    labelKey: 'notifications.channelPushme',
    fields: [
      { key: 'url', labelKey: 'notifications.fieldServerUrl', default: 'https://push.i-i.me' },
      secret('push_key', 'notifications.fieldPushKey')
    ]
  },
  {
    value: 'chronocat',
    labelKey: 'notifications.channelChronocat',
    fields: [
      { key: 'url', labelKey: 'notifications.fieldServerUrl', default: 'http://127.0.0.1:5500' },
      secret('token', 'notifications.fieldToken'),
      {
        key: 'target_type',
        labelKey: 'notifications.fieldTargetType',
        type: 'select',
        default: 'private',
        options: [
          { value: 'private', labelKey: 'notifications.optionPrivate' },
          { value: 'group', labelKey: 'notifications.optionGroup' }
        ]
      },
      text('target', 'notifications.fieldTarget', 'QQ 号或群号')
    ]
  },
  {
    value: 'custom',
    labelKey: 'notifications.channelCustom',
    fields: [
      text('url', 'notifications.fieldUrl'),
      {
        key: 'method',
        labelKey: 'notifications.fieldMethod',
        type: 'select',
        default: 'POST',
        options: [
          { value: 'POST', labelKey: 'notifications.optionPost' },
          { value: 'GET', labelKey: 'notifications.optionGet' }
        ]
      },
      {
        key: 'headers',
        labelKey: 'notifications.fieldHeaders',
        type: 'textarea',
        full: true,
        placeholder: '{"Content-Type":"application/json"}'
      },
      {
        key: 'body',
        labelKey: 'notifications.fieldBody',
        type: 'textarea',
        full: true,
        placeholder: '{"title":"{title}","content":"{content}"}'
      }
    ]
  }
]

const { t } = useI18n()
const channel = ref('mail')
const settings = ref<AppSettings | null>(null)
const savingMail = ref(false)
const testing = ref(false)
const savingGotify = ref(false)
const testingGotify = ref(false)
const savingServerChan = ref(false)
const testingServerChan = ref(false)
const savingGeneric = ref(false)
const testingGeneric = ref(false)

const mail = reactive<MailForm>({
  smtp_host: '',
  smtp_port: 465,
  smtp_username: '',
  smtp_password: '',
  smtp_from: '',
  smtp_ssl: true,
  notify_enabled: false,
  notify_to: ''
})

const gotifyForm = ref<GotifyForm>({
  url: '',
  token: '',
  priority: 5,
  enabled: false
})

const serverChanForm = ref<ServerChanForm>({
  sendkey: '',
  version: 'auto',
  enabled: false
})

const genericForms = ref<Record<string, Record<string, any>>>({})

const currentDef = computed(() => channelDefs.find((item) => item.value === channel.value && item.fields && item.fields.length > 0))
const currentForm = computed(() => genericForms.value[channel.value] || {})

function fieldDefault(field: ChannelField): string | number | boolean {
  if (field.default !== undefined) return field.default
  if (field.type === 'number') return 0
  if (field.type === 'switch') return false
  return ''
}

function applySettings(data: AppSettings): void {
  settings.value = data
  Object.assign(mail, {
    smtp_host: data.smtp_host || '',
    smtp_port: data.smtp_port || 465,
    smtp_username: data.smtp_username || '',
    smtp_password: '',
    smtp_from: data.smtp_from || '',
    smtp_ssl: data.smtp_ssl !== false,
    notify_enabled: !!data.notify_enabled,
    notify_to: data.notify_to || ''
  })
  Object.assign(gotifyForm.value, {
    url: data.gotify_url || '',
    token: '',
    priority: data.gotify_priority ?? 5,
    enabled: !!data.gotify_enabled
  })
  Object.assign(serverChanForm.value, {
    sendkey: '',
    version: (data.serverchan_version as ServerChanForm['version']) || 'auto',
    enabled: !!data.serverchan_enabled
  })

  const storedChannels = data.notify_channels || {}
  const nextForms: Record<string, Record<string, any>> = {}
  for (const def of channelDefs) {
    if (!def.fields || def.fields.length === 0) continue
    const stored = storedChannels[def.value] || {}
    const form: Record<string, any> = {}
    for (const field of def.fields) {
      const value = stored[field.key]
      form[field.key] = value === undefined || value === null || value === '' ? fieldDefault(field) : value
    }
    form.enabled = stored.enabled === true
    nextForms[def.value] = form
  }
  genericForms.value = nextForms
}

async function reload(): Promise<void> {
  try {
    applySettings(await loadSettings(true))
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function saveMail(): Promise<void> {
  savingMail.value = true
  try {
    await api.put('/settings', {
      smtp_host: mail.smtp_host,
      smtp_port: mail.smtp_port,
      smtp_username: mail.smtp_username,
      smtp_password: mail.smtp_password,
      smtp_from: mail.smtp_from,
      smtp_ssl: mail.smtp_ssl,
      notify_enabled: mail.notify_enabled,
      notify_to: mail.notify_to
    })
    ElMessage.success(t('notifications.mailSaved'))
    await reload()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingMail.value = false
  }
}

async function sendTest(): Promise<void> {
  testing.value = true
  try {
    const data = await api.post<MessageResult>('/settings/test-mail', { to: mail.notify_to })
    ElMessage.success(data.message || t('notifications.testSent'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    testing.value = false
  }
}

async function saveGotify(): Promise<void> {
  savingGotify.value = true
  try {
    await api.put('/settings', {
      gotify_url: gotifyForm.value.url,
      gotify_token: gotifyForm.value.token,
      gotify_priority: gotifyForm.value.priority,
      gotify_enabled: gotifyForm.value.enabled
    })
    ElMessage.success(t('notifications.gotifySaved'))
    await reload()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingGotify.value = false
  }
}

async function sendGotifyTest(): Promise<void> {
  testingGotify.value = true
  try {
    const data = await api.post<MessageResult>('/settings/test-gotify', {
      url: gotifyForm.value.url,
      token: gotifyForm.value.token,
      priority: gotifyForm.value.priority
    })
    ElMessage.success(data.message || t('notifications.gotifyTestSent'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    testingGotify.value = false
  }
}

async function saveServerChan(): Promise<void> {
  savingServerChan.value = true
  try {
    await api.put('/settings', {
      serverchan_sendkey: serverChanForm.value.sendkey,
      serverchan_version: serverChanForm.value.version,
      serverchan_enabled: serverChanForm.value.enabled
    })
    ElMessage.success(t('notifications.serverchanSaved'))
    await reload()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingServerChan.value = false
  }
}

async function sendServerChanTest(): Promise<void> {
  testingServerChan.value = true
  try {
    const data = await api.post<MessageResult>('/settings/test-serverchan', {
      sendkey: serverChanForm.value.sendkey,
      version: serverChanForm.value.version
    })
    ElMessage.success(data.message || t('notifications.serverchanTestSent'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    testingServerChan.value = false
  }
}

async function saveGeneric(): Promise<void> {
  if (!currentDef.value) return
  savingGeneric.value = true
  try {
    await api.put('/settings', {
      notify_channels: { [channel.value]: currentForm.value }
    })
    ElMessage.success(t('notifications.channelSaved'))
    await reload()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingGeneric.value = false
  }
}

async function testGeneric(): Promise<void> {
  if (!currentDef.value) return
  testingGeneric.value = true
  try {
    const data = await api.post<MessageResult>('/settings/test-channel', {
      channel: channel.value,
      config: currentForm.value
    })
    ElMessage.success(data.message || t('notifications.channelTestSent'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    testingGeneric.value = false
  }
}

onMounted(reload)
</script>

<style scoped>
.filter-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}
.filter-row :deep(.el-radio-group) {
  display: flex;
  flex-wrap: wrap;
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: -8px;
  margin-bottom: 14px;
  line-height: 1.6;
}
</style>
