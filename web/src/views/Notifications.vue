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
        <el-radio-button v-for="item in channelDefs" :key="item.value" :value="item.value">{{ channelLabel(item) }}</el-radio-button>
      </el-radio-group>
      <span class="muted">{{ t('notifications.channelHint') }}</span>
    </div>
    <el-alert
      v-if="settings && !(settings.notify_channel_defs || []).length"
      :title="t('notifications.noPluginChannels')"
      type="info"
      :closable="false"
      style="margin-bottom: 14px"
    />

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

        <ReauthFields
          v-model:password="reauth.password"
          v-model:totp="reauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />

        <div class="flex gap-10 wrap">
          <el-button type="primary" :loading="savingMail" @click="saveMail">{{ t('notifications.saveMail') }}</el-button>
          <el-button :loading="testingMail" @click="sendTest">{{ t('notifications.sendTest') }}</el-button>
          <span class="muted" style="align-self: center; font-size: 12px">{{ t('notifications.testHint') }}</span>
        </div>
      </el-form>
    </div>

    <div v-else-if="currentDef" class="k-card">
      <div class="card-head">
        <div>
          <h3>{{ channelLabel(currentDef) }}</h3>
          <div class="sub">{{ t('notifications.genericSub', { name: channelLabel(currentDef) }) }}</div>
        </div>
      </div>
      <el-form :model="currentForm" label-position="top">
        <div class="grid grid-3" style="gap: 0 18px">
          <el-form-item
            v-for="field in currentDef.fields"
            :key="field.key"
            :label="fieldLabel(field)"
            :style="field.type === 'textarea' ? 'grid-column: 1 / -1' : undefined"
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
              style="width: 100%"
            />
            <el-select v-else-if="field.type === 'select'" v-model="currentForm[field.key]" style="width: 100%">
              <el-option
                v-for="option in field.options"
                :key="option.value"
                :value="option.value"
                :label="option.label_key ? t(option.label_key) : option.label || option.value"
              />
            </el-select>
            <el-input
              v-else-if="field.type === 'textarea'"
              v-model="currentForm[field.key]"
              type="textarea"
              :rows="3"
              :placeholder="field.placeholder"
            />
            <el-switch v-else-if="field.type === 'switch'" v-model="currentForm[field.key]" />
            <div v-if="field.help" class="form-help">{{ field.help }}</div>
          </el-form-item>
          <el-form-item :label="t('notifications.channelEnabled')">
            <div class="flex gap-10" style="height: 32px">
              <el-switch v-model="currentForm.enabled" />
            </div>
          </el-form-item>
        </div>

        <ReauthFields
          v-model:password="reauth.password"
          v-model:totp="reauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />

        <div class="flex gap-10 wrap">
          <el-button type="primary" :loading="savingGeneric" @click="saveGeneric">{{ t('notifications.saveChannel') }}</el-button>
          <el-button :loading="testingGeneric" @click="testGeneric">{{ t('notifications.testChannel') }}</el-button>
          <span class="muted" style="align-self: center; font-size: 12px">{{ t('notifications.channelTestHint') }}</span>
        </div>
      </el-form>
    </div>

    <el-empty v-else :description="t('notifications.noPluginChannels')" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { loadSettings, state } from '../store'
import { notifyChannelLabelKey } from '../notifyChannels'
import ReauthFields from '../components/ReauthFields.vue'
import type { MessageResult, NotifyChannelField, Settings as AppSettings } from '../types'

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

interface ChannelDef {
  value: string
  labelKey?: string
  label?: string
  fields?: NotifyChannelField[]
}

const text = (key: string, labelKey: string, placeholder?: string): NotifyChannelField => ({ key, label_key: labelKey, placeholder })
const secret = (key: string, labelKey: string): NotifyChannelField => ({ key, label_key: labelKey, type: 'password' })

// 字段级本地化兜底：插件可以只提供 label，官方渠道使用 label_key。
const fieldLabelKeys: Record<string, string> = {
  url: 'notifications.fieldUrl',
  server_url: 'notifications.fieldServerUrl',
  token: 'notifications.fieldToken',
  access_token: 'notifications.fieldAccessToken',
  topic: 'notifications.fieldTopic',
  priority: 'notifications.fieldPriority',
  pushkey: 'notifications.fieldPushKey',
  push_key: 'notifications.fieldPushKey',
  key: 'notifications.fieldKey',
  sound: 'notifications.fieldSound',
  group: 'notifications.fieldGroup',
  bot_token: 'notifications.fieldBotToken',
  chat_id: 'notifications.fieldChatId',
  webhook: 'notifications.fieldWebhook',
  secret: 'notifications.fieldSecret',
  agent_id: 'notifications.fieldAgentId',
  corp_id: 'notifications.fieldCorpId',
  corp_secret: 'notifications.fieldCorpSecret',
  to_user: 'notifications.fieldToUser',
  target_type: 'notifications.fieldTargetType',
  target_id: 'notifications.fieldTargetId',
  target: 'notifications.fieldTarget',
  uids: 'notifications.fieldUids',
  topic_ids: 'notifications.fieldTopicIds',
  method: 'notifications.fieldMethod',
  headers: 'notifications.fieldHeaders',
  body: 'notifications.fieldBody',
  app_token: 'notifications.fieldAppToken',
  spt: 'notifications.fieldSpt',
  sendkey: 'notifications.fieldSendKey',
  version: 'notifications.fieldVersion'
}

// 没有插件元数据时的官方渠道字段定义，用于旧插件清单缺少字段的兼容场景。
const legacyFieldDefs: Record<string, NotifyChannelField[]> = {
  ntfy: [text('url', 'notifications.fieldServerUrl', 'https://ntfy.sh'), text('topic', 'notifications.fieldTopic'), secret('token', 'notifications.fieldToken'), { key: 'priority', label_key: 'notifications.fieldPriority', type: 'number' }],
  gocqhttp: [text('url', 'notifications.fieldServerUrl', 'http://127.0.0.1:5700'), secret('access_token', 'notifications.fieldAccessToken'), text('target_type', 'notifications.fieldTargetType'), text('target_id', 'notifications.fieldTargetId')],
  pushdeer: [text('url', 'notifications.fieldServerUrl', 'https://api2.pushdeer.com'), secret('pushkey', 'notifications.fieldPushKey')],
  bark: [text('url', 'notifications.fieldServerUrl', 'https://api.day.app'), secret('key', 'notifications.fieldKey'), text('sound', 'notifications.fieldSound'), text('group', 'notifications.fieldGroup')],
  telegram: [secret('bot_token', 'notifications.fieldBotToken'), text('chat_id', 'notifications.fieldChatId')],
  dingtalk: [secret('webhook', 'notifications.fieldWebhook'), secret('secret', 'notifications.fieldSecret')],
  wecom_bot: [secret('webhook', 'notifications.fieldWebhook')],
  wecom_app: [text('corp_id', 'notifications.fieldCorpId'), secret('corp_secret', 'notifications.fieldCorpSecret'), { key: 'agent_id', label_key: 'notifications.fieldAgentId', type: 'number' }, text('to_user', 'notifications.fieldToUser')],
  wecom_smart: [text('url', 'notifications.fieldUrl'), secret('token', 'notifications.fieldToken')],
  igot: [secret('key', 'notifications.fieldKey')],
  pushplus: [secret('token', 'notifications.fieldToken'), text('topic', 'notifications.fieldTopic')],
  weplus: [text('url', 'notifications.fieldUrl'), secret('token', 'notifications.fieldToken')],
  wxpusher: [secret('app_token', 'notifications.fieldAppToken'), text('uids', 'notifications.fieldUids'), text('topic_ids', 'notifications.fieldTopicIds')],
  wxpusher_spt: [secret('spt', 'notifications.fieldSpt')],
  openilink: [text('url', 'notifications.fieldUrl'), secret('token', 'notifications.fieldToken')],
  wpush: [text('url', 'notifications.fieldUrl'), secret('token', 'notifications.fieldToken')],
  synology_chat: [secret('url', 'notifications.fieldWebhook')],
  feishu: [secret('webhook', 'notifications.fieldWebhook'), secret('secret', 'notifications.fieldSecret')],
  pushme: [text('url', 'notifications.fieldServerUrl', 'https://push.i-i.me'), secret('push_key', 'notifications.fieldPushKey')],
  chronocat: [text('url', 'notifications.fieldServerUrl', 'http://127.0.0.1:5500'), secret('token', 'notifications.fieldToken'), text('target_type', 'notifications.fieldTargetType'), text('target', 'notifications.fieldTarget')],
  custom: [text('url', 'notifications.fieldUrl'), text('method', 'notifications.fieldMethod'), { key: 'headers', label_key: 'notifications.fieldHeaders', type: 'textarea' }, { key: 'body', label_key: 'notifications.fieldBody', type: 'textarea' }]
}

const { t } = useI18n()
const channel = ref('mail')
const settings = ref<AppSettings | null>(null)
const savingMail = ref(false)
const testingMail = ref(false)
const savingGeneric = ref(false)
const testingGeneric = ref(false)
const user = computed(() => state.user)
const reauth = reactive({ password: '', totp: '' })

function validReauth(form: { password: string; totp: string }): boolean {
  if (user.value?.totp_enabled) {
    if (!/^\d{6}$/.test(form.totp.trim())) {
      ElMessage.warning(t('settings.reauthTotpRequired'))
      return false
    }
    return true
  }
  if (!form.password) {
    ElMessage.warning(t('settings.reauthPasswordRequired'))
    return false
  }
  return true
}

function clearReauth(): void {
  reauth.password = ''
  reauth.totp = ''
}

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

const channelDefs = computed<ChannelDef[]>(() => {
  const defs: ChannelDef[] = [{ value: 'mail', labelKey: 'notifications.channelMail' }]
  for (const def of settings.value?.notify_channel_defs || []) {
    const labelKey = notifyChannelLabelKey(def.id)
    const fields = (def.fields && def.fields.length > 0 ? def.fields : legacyFieldDefs[def.id] || []).map((field) => ({
      ...field,
      label_key: field.label_key || fieldLabelKeys[field.key]
    }))
    defs.push({ value: def.id, labelKey, label: def.name || def.id, fields })
  }
  return defs
})

const currentDef = computed(() => {
  const def = channelDefs.value.find((item) => item.value === channel.value)
  return def && def.value !== 'mail' ? def : undefined
})

const genericForms = ref<Record<string, Record<string, any>>>({})
const currentForm = computed(() => genericForms.value[channel.value] || {})

function channelLabel(def: ChannelDef): string {
  return def.labelKey ? t(def.labelKey) : def.label || def.value
}

function fieldLabel(field: NotifyChannelField): string {
  if (field.label_key) return t(field.label_key)
  return field.label || field.key
}

function fieldDefault(field: NotifyChannelField): string | number | boolean {
  if (field.default !== undefined && field.default !== '') {
    if (field.type === 'number') {
      const value = Number(field.default)
      return Number.isFinite(value) ? value : 0
    }
    if (field.type === 'switch') return field.default === 'true'
    return field.default
  }
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

  const storedChannels = data.notify_channels || {}
  const nextForms: Record<string, Record<string, any>> = {}
  for (const def of channelDefs.value) {
    if (!def.fields || def.fields.length === 0) continue
    const stored = storedChannels[def.value] || {}
    const form: Record<string, any> = {}
    for (const field of def.fields) {
      const value = stored[field.key]
      if (value === undefined || value === null || value === '') {
        form[field.key] = fieldDefault(field)
      } else if (field.type === 'number') {
        const number = Number(value)
        form[field.key] = Number.isFinite(number) ? number : fieldDefault(field)
      } else {
        form[field.key] = value
      }
    }
    form.enabled = stored.enabled === true
    nextForms[def.value] = form
  }
  genericForms.value = nextForms
  if (!channelDefs.value.some((item) => item.value === channel.value)) {
    channel.value = 'mail'
  }
}

async function reload(): Promise<void> {
  try {
    applySettings(await loadSettings(true))
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function saveMail(): Promise<void> {
  if (!validReauth(reauth)) return
  savingMail.value = true
  try {
    const saved = await api.put('/settings', {
        smtp_host: mail.smtp_host,
        smtp_port: mail.smtp_port,
        smtp_username: mail.smtp_username,
        smtp_password: mail.smtp_password,
        smtp_from: mail.smtp_from,
        smtp_ssl: mail.smtp_ssl,
        notify_enabled: mail.notify_enabled,
        notify_to: mail.notify_to,
        password: reauth.password,
        totp: reauth.totp
      })
    if (saved === null) return
    clearReauth()
    ElMessage.success(t('notifications.mailSaved'))
    await reload()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingMail.value = false
  }
}

async function sendTest(): Promise<void> {
  if (!validReauth(reauth)) return
  testingMail.value = true
  try {
    const data = await api.post<MessageResult>('/settings/test-mail', {
      to: mail.notify_to,
      password: reauth.password,
      totp: reauth.totp
    })
    if (data === null) return
    clearReauth()
    ElMessage.success(data.message || t('notifications.testSent'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    testingMail.value = false
  }
}

async function saveGeneric(): Promise<void> {
  if (!currentDef.value) return
  if (!validReauth(reauth)) return
  savingGeneric.value = true
  try {
    const saved = await api.put('/settings', {
        notify_channels: { [channel.value]: currentForm.value },
        password: reauth.password,
        totp: reauth.totp
      })
    if (saved === null) return
    clearReauth()
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
  if (!validReauth(reauth)) return
  testingGeneric.value = true
  try {
    const data = await api.post<MessageResult>('/settings/test-channel', {
        channel: channel.value,
        config: currentForm.value,
        password: reauth.password,
        totp: reauth.totp
      })
    if (data === null) return
    clearReauth()
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
  margin-top: 4px;
  line-height: 1.6;
}
</style>
