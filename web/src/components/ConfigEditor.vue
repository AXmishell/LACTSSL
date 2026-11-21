<template>
  <el-form :model="modelValue" label-position="top" :disabled="disabled" class="config-editor">
    <el-tabs v-model="tab" class="config-tabs">
      <el-tab-pane :label="t('setup.configNetwork')" name="network">
        <el-form-item :label="t('setup.configAddr')">
          <el-input v-model="modelValue.server.addr" placeholder=":8080" />
          <div class="form-help">{{ t('setup.configAddrHint') }}</div>
        </el-form-item>
        <div class="config-grid">
          <el-form-item :label="t('setup.configHost')">
            <el-input v-model="modelValue.server.host" placeholder="0.0.0.0" />
          </el-form-item>
          <el-form-item :label="t('setup.configPort')">
            <el-input-number
              v-model="modelValue.server.port"
              :min="1"
              :max="65535"
              controls-position="right"
              style="width: 100%"
            />
          </el-form-item>
        </div>
        <el-form-item :label="t('setup.configTrustedProxies')">
          <div class="proxy-row">
            <el-input v-model="trustedProxiesText" placeholder="127.0.0.1, 10.0.0.0/8" />
            <el-button v-if="showDetect" :icon="Aim" :disabled="disabled" @click="emit('detect-proxy')">
              {{ t('settings.configDetectProxy') }}
            </el-button>
          </div>
          <div class="form-help">{{ t('setup.configTrustedProxiesHint') }}</div>
        </el-form-item>
        <div class="config-grid">
          <el-form-item :label="t('setup.configClientIPMode')">
            <el-select v-model="modelValue.server.client_ip_mode" style="width: 100%">
              <el-option :label="t('setup.configClientIPModeAuto')" value="auto" />
              <el-option :label="t('setup.configClientIPModeCloudflare')" value="cloudflare" />
            </el-select>
            <div class="form-help">{{ t('setup.configClientIPModeHint') }}</div>
          </el-form-item>
          <el-form-item>
            <el-switch
              v-model="modelValue.server.proxy_protocol"
              :active-text="t('setup.configProxyProtocol')"
            />
            <div class="form-help">{{ t('setup.configProxyProtocolHint') }}</div>
          </el-form-item>
        </div>
      </el-tab-pane>
      <el-tab-pane :label="t('setup.configSecurity')" name="security">
        <el-form-item :label="t('setup.configPublicURL')">
          <div class="proxy-row">
            <el-input v-model="modelValue.public_url" placeholder="https://ssl.example.com" />
            <el-button :icon="Aim" :disabled="disabled || !canUseCurrentOrigin" @click="useCurrentOrigin">
              {{ t('setup.configUseCurrentOrigin') }}
            </el-button>
          </div>
          <div class="form-help">{{ t('setup.configPublicURLHint') }}</div>
        </el-form-item>
        <el-form-item>
          <el-switch v-model="modelValue.session_persistence" :active-text="t('setup.configSessionPersistence')" />
          <div class="form-help">{{ t('setup.configSessionPersistenceHint') }}</div>
        </el-form-item>
      </el-tab-pane>
      <el-tab-pane :label="t('setup.configRateLimit')" name="rate_limit">
        <el-form-item :label="t('setup.configRateLimitMaxKeys')">
          <el-input-number
            v-model="modelValue.rate_limit.max_keys"
            :min="1"
            :max="1000000"
            :step="1000"
            controls-position="right"
            style="width: 100%"
          />
          <div class="form-help">{{ t('setup.configRateLimitHint') }}</div>
        </el-form-item>
        <el-form-item :label="t('setup.configRateLimitSearch')">
          <el-input
            v-model="rateLimitQuery"
            clearable
            :placeholder="t('setup.configRateLimitSearchHint')"
          />
        </el-form-item>
        <el-table :data="rateLimitRows" size="small" max-height="420" style="width: 100%">
          <el-table-column :label="t('setup.configRateLimitRule')" min-width="220">
            <template #default="{ row }">
              <span class="mono">{{ row }}</span>
              <div v-if="rateLimitRuleLabel(row)" class="rate-limit-rule-label">
                {{ rateLimitRuleLabel(row) }}
              </div>
              <div v-if="isRateLimitCustomized(row)" class="rate-limit-rule-label">
                {{ t('setup.configRateLimitCustomized') }}
              </div>
            </template>
          </el-table-column>
          <el-table-column :label="t('setup.configRateLimitMax')" width="170">
            <template #default="{ row }">
              <el-input-number
                v-model="modelValue.rate_limit.rules[row].max"
                :min="0"
                :max="1000000"
                size="small"
                controls-position="right"
                style="width: 130px"
                @change="clearRateLimitReset(row)"
              />
            </template>
          </el-table-column>
          <el-table-column :label="t('setup.configRateLimitWindow')" width="180">
            <template #default="{ row }">
              <el-input-number
                v-model="modelValue.rate_limit.rules[row].window_seconds"
                :min="0"
                :max="604800"
                :step="30"
                size="small"
                controls-position="right"
                style="width: 140px"
                @change="clearRateLimitReset(row)"
              />
            </template>
          </el-table-column>
          <el-table-column width="120" align="right" :label="t('common.actions')">
            <template #default="{ row }">
              <el-button text type="primary" size="small" @click="resetRateLimitRule(row)">
                {{ t('setup.configRateLimitReset') }}
              </el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="form-help">{{ t('setup.configRateLimitTableHint') }}</div>
        <div class="form-help">{{ t('setup.configRateLimitResetHint') }}</div>
      </el-tab-pane>
      <el-tab-pane :label="t('setup.configACME')" name="acme">
        <el-form-item>
          <el-switch v-model="modelValue.acme.enabled" :active-text="t('setup.configACMEEnabled')" />
        </el-form-item>
        <el-form-item :label="t('setup.configACMEAddr')">
          <el-input v-model="modelValue.acme.addr" placeholder=":14000" />
          <div class="form-help">{{ t('setup.configACMEAddrHint') }}</div>
        </el-form-item>
        <el-form-item :label="t('setup.configACMEBaseURL')">
          <el-input v-model="modelValue.acme.base_url" placeholder="https://pki.example.com" />
          <div class="form-help">{{ t('setup.configACMEBaseURLHint') }}</div>
        </el-form-item>
        <div class="config-grid">
          <el-form-item :label="t('setup.configACMETLSCert')">
            <el-input v-model="modelValue.acme.tls.cert" placeholder="/data/acme.crt" />
          </el-form-item>
          <el-form-item :label="t('setup.configACMETLSKey')">
            <el-input v-model="modelValue.acme.tls.key" placeholder="/data/acme.key" />
          </el-form-item>
        </div>
        <div class="form-help config-help-block">{{ t('setup.configACMETLSHint') }}</div>
        <el-form-item>
          <el-switch v-model="modelValue.acme.mtls.enabled" :active-text="t('setup.configACMEMTLS')" />
        </el-form-item>
        <el-form-item :label="t('setup.configACMEMTLSClientCA')">
          <el-input v-model="modelValue.acme.mtls.client_ca" placeholder="/data/client-ca.crt" />
          <div class="form-help">{{ t('setup.configACMEMTLSHint') }}</div>
        </el-form-item>
      </el-tab-pane>
      <el-tab-pane :label="t('setup.configLog')" name="log">
        <div class="config-grid">
          <el-form-item :label="t('setup.configLogLevel')">
            <el-select v-model="modelValue.log.level" style="width: 100%">
              <el-option label="debug" value="debug" />
              <el-option label="info" value="info" />
              <el-option label="warn" value="warn" />
              <el-option label="error" value="error" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('setup.configLogFormat')">
            <el-select v-model="modelValue.log.format" style="width: 100%">
              <el-option label="text" value="text" />
              <el-option label="json" value="json" />
            </el-select>
          </el-form-item>
        </div>
        <el-form-item>
          <el-switch v-model="modelValue.log.also_stdout" :active-text="t('setup.configLogAlsoStdout')" />
        </el-form-item>
        <el-form-item>
          <el-switch v-model="modelValue.log.access_log" :active-text="t('setup.configLogAccessLog')" />
        </el-form-item>
        <div class="config-grid">
          <el-form-item :label="t('setup.configLogMaxSizeMB')">
            <el-input-number
              v-model="modelValue.log.max_size_mb"
              :min="1"
              :max="1024"
              controls-position="right"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item :label="t('setup.configLogMaxBackups')">
            <el-input-number
              v-model="modelValue.log.max_backups"
              :min="0"
              :max="100"
              controls-position="right"
              style="width: 100%"
            />
          </el-form-item>
        </div>
        <div class="config-grid">
          <el-form-item :label="t('setup.configLogMaxAgeDays')">
            <el-input-number
              v-model="modelValue.log.max_age_days"
              :min="0"
              :max="3650"
              controls-position="right"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item>
            <el-switch v-model="modelValue.log.compress" :active-text="t('setup.configLogCompress')" />
          </el-form-item>
        </div>

        <div class="config-sub">{{ t('setup.configLogSystem') }}</div>
        <el-form-item>
          <el-switch v-model="modelValue.log.system.enabled" :active-text="t('setup.configLogSystemEnabled')" />
        </el-form-item>
        <div class="config-grid">
          <el-form-item :label="t('setup.configLogSystemLevel')">
            <el-select v-model="modelValue.log.system.level" style="width: 100%">
              <el-option label="debug" value="debug" />
              <el-option label="info" value="info" />
              <el-option label="warn" value="warn" />
              <el-option label="error" value="error" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('setup.configLogSystemRetentionDays')">
            <el-input-number
              v-model="modelValue.log.system.retention_days"
              :min="1"
              :max="365"
              controls-position="right"
              style="width: 100%"
            />
          </el-form-item>
        </div>
        <el-form-item>
          <el-switch v-model="modelValue.log.system.access" :active-text="t('setup.configLogSystemAccess')" />
        </el-form-item>
        <el-form-item :label="t('setup.configLogSystemMaxRows')">
          <el-input-number
            v-model="modelValue.log.system.max_rows"
            :min="100"
            :max="1000000"
            :step="1000"
            controls-position="right"
            style="width: 100%"
          />
        </el-form-item>

        <div class="config-sub">{{ t('setup.configLogArchive') }}</div>
        <el-form-item>
          <el-switch v-model="modelValue.log.archive.enabled" :active-text="t('setup.configLogArchiveEnabled')" />
        </el-form-item>
        <el-form-item>
          <el-switch v-model="modelValue.log.archive.compress" :active-text="t('setup.configLogArchiveCompress')" />
        </el-form-item>
        <div class="config-grid">
          <el-form-item :label="t('setup.configLogArchiveRetentionDays')">
            <el-input-number
              v-model="modelValue.log.archive.retention_days"
              :min="0"
              :max="3650"
              controls-position="right"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item :label="t('setup.configLogArchiveMaxTotalMB')">
            <el-input-number
              v-model="modelValue.log.archive.max_total_mb"
              :min="0"
              :max="1048576"
              :step="100"
              controls-position="right"
              style="width: 100%"
            />
          </el-form-item>
        </div>
        <el-form-item :label="t('setup.configLogArchiveQuotaPolicy')">
          <el-select v-model="modelValue.log.archive.quota_policy" style="width: 100%">
            <el-option :label="t('setup.configLogArchiveQuotaFail')" value="fail" />
            <el-option :label="t('setup.configLogArchiveQuotaPrune')" value="prune_oldest" />
          </el-select>
          <div class="form-help">{{ t('setup.configLogArchiveHint') }}</div>
        </el-form-item>
      </el-tab-pane>
      <el-tab-pane :label="t('setup.configUpgrade')" name="upgrade">
        <el-form-item>
          <el-switch v-model="modelValue.upgrade.auto" :active-text="t('setup.configUpgradeAuto')" />
        </el-form-item>
        <el-form-item>
          <el-switch v-model="modelValue.upgrade.auto_optimize" :active-text="t('setup.configUpgradeAutoOptimize')" />
        </el-form-item>
        <el-form-item>
          <el-switch v-model="modelValue.upgrade.vacuum" :active-text="t('setup.configUpgradeVacuum')" />
        </el-form-item>
        <div class="config-sub">{{ t('setup.configUpgradeProgram') }}</div>
        <el-form-item>
          <el-switch v-model="modelValue.upgrade.program.enabled" :active-text="t('setup.configUpgradeProgramEnabled')" />
        </el-form-item>
        <div class="config-grid">
          <el-form-item :label="t('setup.configChannel')">
            <el-select v-model="modelValue.upgrade.program.channel" style="width: 100%">
              <el-option :label="t('setup.configChannelStable')" value="stable" />
              <el-option :label="t('setup.configChannelBeta')" value="beta" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('setup.configCheckIntervalHours')">
            <el-input-number
              v-model="modelValue.upgrade.program.check_interval_hours"
              :min="1"
              :max="168"
              controls-position="right"
              style="width: 100%"
            />
          </el-form-item>
        </div>
        <el-form-item :label="t('setup.configMaxDownloadMB')">
          <el-input-number
            v-model="modelValue.upgrade.program.max_download_mb"
            :min="8"
            :max="2048"
            controls-position="right"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item>
          <el-switch
            v-model="modelValue.upgrade.program.allow_unsigned"
            :active-text="t('setup.configUpgradeAllowUnsigned')"
          />
          <div class="form-help">{{ t('setup.configUpgradeAllowUnsignedHint') }}</div>
        </el-form-item>
      </el-tab-pane>
      <el-tab-pane :label="t('setup.configPlugins')" name="plugins">
        <div class="config-section">
          <div class="config-section-head">
            <div class="config-section-title-wrap">
              <div class="config-section-title">{{ t('setup.configDNSPluginsTitle') }}</div>
              <div class="config-section-desc">{{ t('setup.configDNSPluginsHint') }}</div>
            </div>
            <el-switch v-model="modelValue.plugins.enabled" :aria-label="t('setup.configDNSPlugins')" />
          </div>
          <div class="config-grid config-grid-3">
            <el-form-item :label="t('setup.configMemoryLimitMB')">
              <el-input-number
                v-model="modelValue.plugins.memory_limit_mb"
                :min="0"
                :max="4096"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('setup.configTimeoutSeconds')">
              <el-input-number
                v-model="modelValue.plugins.timeout_seconds"
                :min="1"
                :max="600"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('setup.configMaxOutputKB')">
              <el-input-number
                v-model="modelValue.plugins.max_output_kb"
                :min="1"
                :max="1024"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
          </div>
        </div>

        <div class="config-section">
          <div class="config-section-head">
            <div class="config-section-title-wrap">
              <div class="config-section-title">{{ t('setup.configPluginRepo') }}</div>
              <div class="config-section-desc">{{ t('setup.configPluginRepoDesc') }}</div>
            </div>
            <el-switch v-model="modelValue.plugins.repo.enabled" :aria-label="t('setup.configPluginRepoEnabled')" />
          </div>
          <el-form-item :label="t('setup.configPluginRepoURL')">
            <el-input
              v-model="modelValue.plugins.repo.url"
              placeholder="https://github.com/AXmishell/LACTSSL-Plugins/releases/download/plugin-index/index.json"
            />
            <div class="form-help">{{ t('setup.configPluginRepoURLHint') }}</div>
          </el-form-item>
          <el-form-item :label="t('setup.configPluginRepoProxy')">
            <el-input v-model="modelValue.plugins.repo.proxy" placeholder="http://127.0.0.1:7890 / socks5://127.0.0.1:1080" />
            <div class="form-help">{{ t('setup.configPluginRepoProxyHint') }}</div>
          </el-form-item>
          <div class="config-grid">
            <el-form-item :label="t('setup.configChannel')">
              <el-select v-model="modelValue.plugins.repo.channel" style="width: 100%">
                <el-option :label="t('setup.configChannelStable')" value="stable" />
                <el-option :label="t('setup.configChannelBeta')" value="beta" />
              </el-select>
            </el-form-item>
            <el-form-item :label="t('setup.configCheckIntervalHours')">
              <el-input-number
                v-model="modelValue.plugins.repo.check_interval_hours"
                :min="1"
                :max="168"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('setup.configMaxDownloadMB')">
              <el-input-number
                v-model="modelValue.plugins.repo.max_download_mb"
                :min="1"
                :max="4096"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('setup.configTimeoutSeconds')">
              <el-input-number
                v-model="modelValue.plugins.repo.timeout_seconds"
                :min="1"
                :max="600"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
          </div>
          <div class="config-switch-row">
            <el-switch v-model="modelValue.plugins.repo.auto_update" :active-text="t('setup.configPluginRepoAutoUpdate')" />
          </div>
        </div>

        <div class="config-section">
          <div class="config-section-head">
            <div class="config-section-title-wrap">
              <div class="config-section-title">{{ t('setup.configDeployPlugins') }}</div>
              <div class="config-section-desc">{{ t('setup.configDeployPluginsHint') }}</div>
            </div>
            <el-switch v-model="modelValue.deploy_plugins.enabled" :aria-label="t('setup.configDeployPluginsEnabled')" />
          </div>
          <div class="config-grid config-grid-3">
            <el-form-item :label="t('setup.configMemoryLimitMB')">
              <el-input-number
                v-model="modelValue.deploy_plugins.memory_limit_mb"
                :min="0"
                :max="4096"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('setup.configTimeoutSeconds')">
              <el-input-number
                v-model="modelValue.deploy_plugins.timeout_seconds"
                :min="1"
                :max="600"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('setup.configMaxOutputKB')">
              <el-input-number
                v-model="modelValue.deploy_plugins.max_output_kb"
                :min="1"
                :max="1024"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
          </div>
        </div>

        <div class="config-section">
          <div class="config-section-head">
            <div class="config-section-title-wrap">
              <div class="config-section-title">{{ t('setup.configNotifyPlugins') }}</div>
              <div class="config-section-desc">{{ t('setup.configNotifyPluginsHint') }}</div>
            </div>
            <el-switch v-model="modelValue.notify_plugins.enabled" :aria-label="t('setup.configNotifyPluginsEnabled')" />
          </div>
          <div class="config-grid config-grid-3">
            <el-form-item :label="t('setup.configMemoryLimitMB')">
              <el-input-number
                v-model="modelValue.notify_plugins.memory_limit_mb"
                :min="0"
                :max="4096"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('setup.configTimeoutSeconds')">
              <el-input-number
                v-model="modelValue.notify_plugins.timeout_seconds"
                :min="1"
                :max="600"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('setup.configMaxOutputKB')">
              <el-input-number
                v-model="modelValue.notify_plugins.max_output_kb"
                :min="1"
                :max="1024"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>
  </el-form>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Aim } from '@element-plus/icons-vue'
import type { SetupConfig } from '../types'

const props = defineProps<{
  modelValue: SetupConfig
  disabled?: boolean
  showDetect?: boolean
}>()

const emit = defineEmits<{
  (event: 'detect-proxy'): void
}>()

const { t, te } = useI18n()

// 浏览器当前访问地址就是管理员实际使用的入口，可作为对外访问地址的默认值。
const browserOrigin = (() => {
  if (typeof window === 'undefined') return ''
  const origin = window.location.origin
  return origin && origin !== 'null' ? origin.replace(/\/+$/, '') : ''
})()

const canUseCurrentOrigin = computed(() => browserOrigin !== '')

function useCurrentOrigin(): void {
  if (!browserOrigin) return
  props.modelValue.public_url = browserOrigin
}

onMounted(() => {
  // 仅在留空时自动带入，避免覆盖已经存在的配置。
  if (!props.modelValue.public_url?.trim()) {
    useCurrentOrigin()
  }
})

const tab = ref('network')
const trustedProxiesText = ref((props.modelValue.trusted_proxies || []).join(', '))
const rateLimitQuery = ref('')

function rateLimitRuleLabel(name: string): string {
  const key = `setup.rateLimitRules.${name}`
  return te(key) ? t(key) : ''
}

function rateLimitResetPath(name: string): string {
  return `security.rate_limit.rules.${name}`
}

function isRateLimitCustomized(name: string): boolean {
  const rule = props.modelValue.rate_limit?.rules?.[name]
  const def = props.modelValue.rate_limit?.defaults?.[name]
  if (!rule || !def) return false
  return rule.max !== def.max || rule.window_seconds !== def.window_seconds
}

function resetRateLimitRule(name: string): void {
  const def = props.modelValue.rate_limit?.defaults?.[name]
  if (!props.modelValue.rate_limit.rules) props.modelValue.rate_limit.rules = {}
  if (def) props.modelValue.rate_limit.rules[name] = { ...def }
  if (!props.modelValue.reset) props.modelValue.reset = []
  const path = rateLimitResetPath(name)
  if (!props.modelValue.reset.includes(path)) props.modelValue.reset.push(path)
}

function clearRateLimitReset(name: string): void {
  if (!props.modelValue.reset) return
  const path = rateLimitResetPath(name)
  props.modelValue.reset = props.modelValue.reset.filter((item) => item !== path)
}

const rateLimitRows = computed(() => {
  const rules = props.modelValue.rate_limit?.rules || {}
  const query = rateLimitQuery.value.trim().toLowerCase()
  return Object.keys(rules)
    .filter((name) => {
      if (!query) return true
      return (
        name.toLowerCase().includes(query) ||
        rateLimitRuleLabel(name).toLowerCase().includes(query)
      )
    })
    .sort()
})

watch(
  () => props.modelValue.rate_limit,
  (value) => {
    if (!value) {
      props.modelValue.rate_limit = { max_keys: 10000, rules: {}, defaults: {} }
      return
    }
    if (!value.rules) value.rules = {}
    if (!value.defaults) value.defaults = {}
    if (!value.max_keys) value.max_keys = 10000
  },
  { immediate: true }
)

watch(
  () => props.modelValue,
  (value) => {
    if (!value.reset) value.reset = []
  },
  { immediate: true }
)

function parseProxyText(value: string): string[] {
  return value
    .split(/[\s,]+/)
    .map((item) => item.trim())
    .filter((item) => item !== '')
}

function sameProxies(left: string[], right: string[]): boolean {
  if (left.length !== right.length) return false
  return left.every((value, index) => value === right[index])
}

watch(
  () => props.modelValue.trusted_proxies,
  (values) => {
    const list = values || []
    if (!sameProxies(list, parseProxyText(trustedProxiesText.value))) {
      trustedProxiesText.value = list.join(', ')
    }
  },
  { immediate: true, deep: true }
)

watch(trustedProxiesText, (value) => {
  const items = parseProxyText(value)
  if (!sameProxies(items, props.modelValue.trusted_proxies || [])) {
    props.modelValue.trusted_proxies = items
  }
})
</script>

<style scoped>
.config-tabs :deep(.el-tabs__header) {
  --el-tabs-header-height: 30px;
  margin-bottom: 18px;
}
.config-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px 16px;
}
.config-grid-3 {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
.config-sub {
  font-size: 12px;
  font-weight: 600;
  color: #475569;
  margin: 4px 0 10px;
}
/* 外部插件按功能分组展示：标题 + 开关置于组头，资源限制统一三列排布。 */
.config-section {
  border: 1px solid var(--kp-border);
  border-radius: 12px;
  background: #fbfcfe;
  padding: 16px 16px 2px;
}
.config-section + .config-section {
  margin-top: 14px;
}
.config-section-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}
.config-section-title-wrap {
  min-width: 0;
}
.config-section-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--kp-text);
}
.config-section-desc {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--kp-muted);
}
.config-section-head .el-switch {
  flex-shrink: 0;
  margin-top: 2px;
}
.config-section :deep(.el-form-item) {
  margin-bottom: 14px;
}
.config-switch-row {
  padding-bottom: 14px;
}
.config-help-block {
  margin: -6px 0 12px;
}
.proxy-row {
  display: flex;
  gap: 8px;
  width: 100%;
}
.proxy-row .el-input {
  flex: 1;
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 6px;
  line-height: 1.6;
}
.rate-limit-rule-label {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 2px;
  line-height: 1.4;
}
@media (max-width: 900px) {
  .config-grid-3 {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }
}
@media (max-width: 640px) {
  .config-grid,
  .config-grid-3 {
    grid-template-columns: minmax(0, 1fr);
  }
  .proxy-row {
    flex-direction: column;
  }
}
</style>
