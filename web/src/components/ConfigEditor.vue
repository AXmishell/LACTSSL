<template>
  <el-form :model="modelValue" label-position="top" :disabled="disabled" class="config-editor">
    <div class="config-section">{{ t('setup.configNetwork') }}</div>
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

    <el-divider />
    <div class="config-section">{{ t('setup.configSecurity') }}</div>
    <el-form-item :label="t('setup.configPublicURL')">
      <el-input v-model="modelValue.public_url" placeholder="https://ssl.example.com" />
      <div class="form-help">{{ t('setup.configPublicURLHint') }}</div>
    </el-form-item>
    <el-form-item>
      <el-switch v-model="modelValue.session_persistence" :active-text="t('setup.configSessionPersistence')" />
      <div class="form-help">{{ t('setup.configSessionPersistenceHint') }}</div>
    </el-form-item>

    <el-divider />
    <div class="config-section">{{ t('setup.configACME') }}</div>
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

    <el-divider />
    <div class="config-section">{{ t('setup.configLog') }}</div>
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

    <el-divider />
    <div class="config-section">{{ t('setup.configUpgrade') }}</div>
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

    <el-divider />
    <div class="config-section">{{ t('setup.configPlugins') }}</div>
    <el-form-item>
      <el-switch v-model="modelValue.plugins.enabled" :active-text="t('setup.configDNSPlugins')" />
    </el-form-item>
    <div class="config-grid">
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
    </div>
    <el-form-item :label="t('setup.configMaxOutputKB')">
      <el-input-number
        v-model="modelValue.plugins.max_output_kb"
        :min="1"
        :max="1024"
        controls-position="right"
        style="width: 100%"
      />
    </el-form-item>

    <div class="config-sub">{{ t('setup.configPluginRepo') }}</div>
    <el-form-item>
      <el-switch v-model="modelValue.plugins.repo.enabled" :active-text="t('setup.configPluginRepoEnabled')" />
    </el-form-item>
    <el-form-item :label="t('setup.configPluginRepoURL')">
      <el-input
        v-model="modelValue.plugins.repo.url"
        placeholder="https://github.com/AXmishell/LACTSSL-Plugins/releases/download/plugin-index/index.json"
      />
      <div class="form-help">{{ t('setup.configPluginRepoURLHint') }}</div>
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
    </div>
    <div class="config-grid">
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
    <el-form-item>
      <el-switch v-model="modelValue.plugins.repo.auto_update" :active-text="t('setup.configPluginRepoAutoUpdate')" />
    </el-form-item>

    <div class="config-sub">{{ t('setup.configDeployPlugins') }}</div>
    <el-form-item>
      <el-switch v-model="modelValue.deploy_plugins.enabled" :active-text="t('setup.configDeployPluginsEnabled')" />
      <div class="form-help">{{ t('setup.configDeployPluginsHint') }}</div>
    </el-form-item>
    <div class="config-grid">
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
    </div>
    <el-form-item :label="t('setup.configMaxOutputKB')">
      <el-input-number
        v-model="modelValue.deploy_plugins.max_output_kb"
        :min="1"
        :max="1024"
        controls-position="right"
        style="width: 100%"
      />
    </el-form-item>

    <div class="config-sub">{{ t('setup.configNotifyPlugins') }}</div>
    <el-form-item>
      <el-switch v-model="modelValue.notify_plugins.enabled" :active-text="t('setup.configNotifyPluginsEnabled')" />
    </el-form-item>
    <div class="config-grid">
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
    </div>
    <el-form-item :label="t('setup.configMaxOutputKB')">
      <el-input-number
        v-model="modelValue.notify_plugins.max_output_kb"
        :min="1"
        :max="1024"
        controls-position="right"
        style="width: 100%"
      />
    </el-form-item>
  </el-form>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
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

const { t } = useI18n()

const trustedProxiesText = ref((props.modelValue.trusted_proxies || []).join(', '))

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
.config-editor :deep(.el-divider) {
  margin: 24px 0 18px;
}
.config-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
}
.config-section {
  font-size: 13px;
  font-weight: 600;
  color: #334155;
  margin-bottom: 10px;
}
.config-sub {
  font-size: 12px;
  font-weight: 600;
  color: #475569;
  margin: 4px 0 10px;
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
@media (max-width: 640px) {
  .config-grid {
    grid-template-columns: minmax(0, 1fr);
  }
  .proxy-row {
    flex-direction: column;
  }
}
</style>
