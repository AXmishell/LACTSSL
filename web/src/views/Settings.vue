<template>
  <div class="page settings-page">
    <div class="page-head">
      <div>
        <h1>{{ t('settings.title') }}</h1>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle @click="reload" />
      </div>
    </div>

    <el-tabs v-model="tab" class="settings-tabs">
      <!-- 账户安全 -->
      <el-tab-pane :label="t('settings.tabSecurity')" name="security">
        <div class="k-card settings-card">
          <div class="card-head">
            <div>
              <h3>{{ t('settings.loginAccount') }}</h3>
              <div class="sub">{{ t('settings.loginAccountSub') }}</div>
            </div>
          </div>

          <div class="settings-scroll">
          <div class="account-row">
            <div style="flex: 1; min-width: 0">
              <div style="font-weight: 600">{{ user?.email }}</div>
              <div class="muted" style="margin-top: 4px">{{ t('settings.adminCreated', { date: formatDate(user?.created_at, false) }) }}</div>
            </div>
            <div class="flex gap-10 wrap">
              <el-button @click="openEmailDialog">{{ t('settings.changeEmail') }}</el-button>
              <el-button @click="openPasswordDialog">{{ t('settings.changePassword') }}</el-button>
            </div>
          </div>

          <div class="setting-row">
            <div>
              <div style="font-weight: 600; font-size: 14px">{{ t('settings.totp') }}</div>
              <div class="muted" style="margin-top: 4px">{{ t('settings.totpSub') }}</div>
            </div>
            <div class="flex gap-10">
              <span class="tag-soft" :class="user?.totp_enabled ? 'tag-green' : 'tag-gray'">
                {{ user?.totp_enabled ? t('settings.enabled') : t('settings.disabled') }}
              </span>
              <el-button v-if="!user?.totp_enabled" type="primary" plain @click="startTOTP">{{ t('settings.enable') }}</el-button>
              <el-button v-else type="danger" plain @click="openDisableTOTP">{{ t('settings.disable') }}</el-button>
            </div>
          </div>

          <div class="setting-row">
            <div>
              <div style="font-weight: 600; font-size: 14px">{{ t('settings.passkeys') }}</div>
              <div class="muted" style="margin-top: 4px">{{ t('settings.passkeysSub') }}</div>
            </div>
            <div class="flex gap-10 wrap" style="align-items: center">
              <span class="tag-soft" :class="passkeys.length ? 'tag-green' : 'tag-gray'">
                {{ passkeys.length ? t('settings.passkeyCount', { count: passkeys.length }) : t('settings.disabled') }}
              </span>
              <el-button type="primary" plain :disabled="!passkeyUsable" @click="openPasskeyDialog">
                {{ t('settings.addPasskey') }}
              </el-button>
            </div>
          </div>
          <div v-if="passkeys.length" class="passkey-list">
            <div v-for="item in passkeys" :key="item.id" class="passkey-item">
              <div style="min-width: 0">
                <div class="passkey-name">{{ item.name }}</div>
                <div class="muted" style="margin-top: 4px; font-size: 12px">
                  {{ t('settings.passkeyCreated', { date: formatDate(item.created_at, false) }) }}
                  <template v-if="item.last_used_at">
                    · {{ t('settings.passkeyLastUsed', { date: formatDate(item.last_used_at, false) }) }}
                  </template>
                </div>
              </div>
              <el-button text type="danger" @click="openDeletePasskey(item)">{{ t('common.delete') }}</el-button>
            </div>
          </div>
          <div v-if="!passkeyUsable" class="form-help">{{ t('settings.passkeyUnavailable') }}</div>

          <div class="setting-row">
            <div>
              <div style="font-weight: 600; font-size: 14px">{{ t('settings.mailRecovery') }}</div>
              <div class="muted" style="margin-top: 4px">{{ t('settings.mailRecoverySub') }}</div>
            </div>
            <el-button @click="goNotifications">{{ t('settings.goConfigure') }}</el-button>
          </div>

          <!-- 门禁入口 -->
          <div class="settings-section">
            <div class="section-head">
              <h3>{{ t('settings.entryTitle') }}</h3>
              <div class="sub">{{ t('settings.entrySub') }}</div>
            </div>
            <el-form :model="entryForm" label-position="top" class="entry-form">
              <el-form-item>
                <el-input v-model="entryForm.entry" :placeholder="settings?.entry || t('settings.entryPlaceholder')" />
                <div class="form-help">{{ t('settings.entryHelp') }}</div>
              </el-form-item>
              <el-button type="primary" :loading="savingEntry" @click="saveEntry">{{ t('settings.updateEntry') }}</el-button>
            </el-form>
          </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- 安全提醒 -->
      <el-tab-pane :label="t('notifications.securityTitle')" name="security-alerts">
        <div class="k-card settings-card">
          <div class="settings-scroll">
            <el-form :model="security" label-position="top">
              <el-form-item :label="t('notifications.securityEnabled')">
                <div class="flex gap-10" style="height: 32px">
                  <el-switch v-model="security.enabled" />
                </div>
              </el-form-item>
              <el-form-item :label="t('notifications.securityLogin')">
                <el-radio-group v-model="security.login">
                  <el-radio-button value="off">{{ t('notifications.securityLoginOff') }}</el-radio-button>
                  <el-radio-button value="new">{{ t('notifications.securityLoginNew') }}</el-radio-button>
                  <el-radio-button value="always">{{ t('notifications.securityLoginAlways') }}</el-radio-button>
                </el-radio-group>
                <div class="form-help">{{ t('notifications.securityLoginHint') }}</div>
              </el-form-item>
              <div class="grid grid-3" style="gap: 0 18px">
                <el-form-item :label="t('notifications.securityNewDeviceDays')">
                  <el-input-number v-model="security.new_device_days" :min="1" :max="365" style="width: 100%" />
                </el-form-item>
                <el-form-item :label="t('notifications.securityFailedLogin')">
                  <div class="flex gap-10" style="height: 32px">
                    <el-switch v-model="security.failed_login" />
                  </div>
                </el-form-item>
                <el-form-item :label="t('notifications.securityFailedThreshold')">
                  <el-input-number
                    v-model="security.failed_threshold"
                    :min="1"
                    :max="100"
                    :disabled="!security.failed_login"
                    style="width: 100%"
                  />
                </el-form-item>
                <el-form-item :label="t('notifications.securityFailedWindow')">
                  <el-input-number
                    v-model="security.failed_window"
                    :min="60"
                    :max="86400"
                    :step="60"
                    :disabled="!security.failed_login"
                    style="width: 100%"
                  />
                </el-form-item>
                <el-form-item :label="t('notifications.securityMaxPerHour')">
                  <el-input-number v-model="security.max_per_hour" :min="1" :max="1000" style="width: 100%" />
                  <div class="form-help">{{ t('notifications.securityMaxPerHourHint') }}</div>
                </el-form-item>
              </div>
              <el-form-item :label="t('notifications.securityChannels')">
                <el-select v-model="security.channels" multiple style="width: 100%">
                  <el-option
                    v-for="item in securityChannelOptions"
                    :key="item.value"
                    :value="item.value"
                    :label="item.label"
                  />
                </el-select>
                <div class="form-help">{{ t('notifications.securityChannelsHint') }}</div>
              </el-form-item>
              <el-form-item :label="t('notifications.securityEvents')">
                <el-checkbox-group v-model="security.events" class="security-events">
                  <el-checkbox v-for="item in securityEventOptions" :key="item.value" :value="item.value">
                    {{ t(item.labelKey) }}
                  </el-checkbox>
                </el-checkbox-group>
                <div class="form-help">{{ t('notifications.securityEventsHint') }}</div>
              </el-form-item>
              <div class="grid grid-2" style="gap: 0 18px">
                <el-form-item :label="t('notifications.securityTo')">
                  <el-input v-model="security.to" :placeholder="t('notifications.securityToPlaceholder')" />
                  <div class="form-help">{{ t('notifications.securityToHint') }}</div>
                </el-form-item>
                <el-form-item :label="t('notifications.securityRetention')">
                  <el-input-number v-model="security.retention_days" :min="7" :max="365" style="width: 100%" />
                </el-form-item>
              </div>
              <el-alert
                v-if="!security.channels.length"
                :title="t('notifications.securityNoChannel')"
                type="warning"
                :closable="false"
                style="margin-bottom: 14px"
              />
              <el-alert
                v-else-if="security.channels.includes('mail') && !settings?.smtp_host"
                :title="t('notifications.securityNoSmtp')"
                type="warning"
                :closable="false"
                style="margin-bottom: 14px"
              />
              <el-alert
                v-else-if="security.last_error"
                :title="t('notifications.securityLastError', { error: security.last_error })"
                type="error"
                :closable="false"
                style="margin-bottom: 14px"
              />
              <el-alert
                v-else-if="security.last_delivered_at"
                :title="t('notifications.securityLastDelivered', { time: formatDate(security.last_delivered_at) })"
                type="success"
                :closable="false"
                style="margin-bottom: 14px"
              />
            </el-form>
          </div>

          <div class="settings-footer">
            <div class="footer-actions">
              <el-button type="primary" :loading="savingSecurity" @click="saveSecurity">
                {{ t('notifications.securitySave') }}
              </el-button>
              <el-button :loading="testingSecurity" @click="sendSecurityTest">
                {{ t('notifications.securitySendTest') }}
              </el-button>
              <el-button @click="openSecurityEvents">{{ t('notifications.securityEventsOpen') }}</el-button>
              <span class="muted test-hint">{{ t('notifications.securityHint') }}</span>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- 备份与恢复 -->
      <el-tab-pane :label="t('settings.tabBackup')" name="backup">
        <div class="k-card settings-card backup-card">
          <div class="settings-scroll">
            <div class="setting-row">
              <div>
                <div class="setting-name">{{ t('settings.backupDownloadTitle') }}</div>
                <div class="muted" style="margin-top: 4px">{{ t('settings.backupDownloadSub') }}</div>
              </div>
            </div>

            <div class="setting-row">
              <div>
                <div class="setting-name">{{ t('settings.backupRestoreTitle') }}</div>
                <div class="muted" style="margin-top: 4px">{{ t('settings.backupRestoreSub') }}</div>
              </div>
            </div>

            <div class="backup-meta">
              <div class="about-line">
                <span>{{ t('settings.backupDatabaseSize') }}</span>
                <span class="mono">{{ backup ? formatBytes(backup.database_size) : t('common.none') }}</span>
              </div>
              <div class="about-line">
                <span>{{ t('settings.backupConfigFile') }}</span>
                <span class="tag-soft" :class="backup?.config_included ? 'tag-green' : 'tag-gray'">
                  {{ backup?.config_included ? t('settings.backupConfigIncluded') : t('settings.backupConfigMissing') }}
                </span>
              </div>
              <div class="about-line">
                <span>{{ t('settings.backupLastBackup') }}</span>
                <span class="mono">{{ formatDate(backup?.last_backup_at) }}</span>
              </div>
            </div>

            <el-alert
              v-if="backup?.pending_restore"
              type="warning"
              :closable="false"
              show-icon
              :title="t('settings.backupPending')"
              style="margin-top: 16px"
            />
            <el-alert
              type="warning"
              :closable="false"
              show-icon
              :title="t('settings.backupWarning')"
              style="margin-top: 16px"
            />
          </div>

          <div class="settings-footer">
            <div class="footer-actions">
              <div class="footer-action-group">
                <el-button type="primary" :loading="backupDownloading" @click="openBackupDialog">
                  {{ t('settings.backupDownload') }}
                </el-button>
              </div>
              <div class="footer-action-group">
                <el-button type="danger" plain @click="openRestore">{{ t('settings.backupRestore') }}</el-button>
              </div>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- 配置文件 -->
      <el-tab-pane :label="t('settings.tabConfig')" name="config">
        <div class="k-card settings-card config-card">
          <div class="settings-scroll">
            <el-alert v-if="configLoadError" :title="configLoadError" type="error" :closable="false" show-icon />
            <el-alert
              v-else-if="configReady && !configWritable"
              type="warning"
              :closable="false"
              show-icon
              :title="t('settings.configReadonly')"
              style="margin-bottom: 16px"
            />
            <el-alert
              v-if="configPending"
              type="warning"
              :closable="false"
              show-icon
              :title="t('settings.configRestartPending')"
              style="margin-bottom: 16px"
            >
              <div style="display: flex; align-items: center; gap: 12px; flex-wrap: wrap">
                <span>{{ t('settings.configRestartPendingHint') }}</span>
                <el-button type="warning" plain size="small" @click="openConfigRestart">
                  {{ t('settings.configRestartNow') }}
                </el-button>
              </div>
            </el-alert>

            <div v-if="configReady && configForm" v-loading="configLoading">
              <ConfigEditor
                :model-value="configForm"
                :disabled="!configWritable"
                show-detect
                @detect-proxy="openProxyDialog"
              />

              <div class="settings-section">
                <div class="section-head">
                  <h3>{{ t('settings.configBackupTitle') }}</h3>
                  <div class="sub">{{ t('settings.configBackupSub') }}</div>
                </div>
                <el-alert
                  type="warning"
                  :closable="false"
                  show-icon
                  :title="t('settings.configBackupWarning')"
                  style="margin-bottom: 16px"
                />
                <div class="table-scroll">
                  <el-table :data="configBackups" v-loading="configLoading" size="small" style="width: 100%">
                    <el-table-column width="170" :label="t('settings.configBackupTime')">
                      <template #default="{ row }">
                        <span>{{ formatDate(row.created_at) }}</span>
                      </template>
                    </el-table-column>
                    <el-table-column width="110" :label="t('settings.configBackupReason')">
                      <template #default="{ row }">
                        <span class="tag-soft tag-gray">{{ configBackupReason(row.reason) }}</span>
                      </template>
                    </el-table-column>
                    <el-table-column min-width="160" :label="t('settings.configBackupNote')">
                      <template #default="{ row }">
                        <span :title="row.note || undefined">{{ row.note || t('common.none') }}</span>
                      </template>
                    </el-table-column>
                    <el-table-column width="110" :label="t('settings.configBackupSize')">
                      <template #default="{ row }">
                        <span class="mono">{{ formatBytes(row.size) }}</span>
                      </template>
                    </el-table-column>
                    <el-table-column width="120" :label="t('settings.configBackupSalt')">
                      <template #default="{ row }">
                        <span class="tag-soft" :class="row.salt_matches ? 'tag-green' : 'tag-red'">
                          {{ row.salt_matches ? t('settings.configBackupSaltMatch') : t('settings.configBackupSaltMismatch') }}
                        </span>
                      </template>
                    </el-table-column>
                    <el-table-column width="210" align="right" :label="t('common.actions')">
                      <template #default="{ row }">
                        <el-button text type="primary" size="small" @click="openBackupAction('restore', row)">
                          {{ t('settings.configBackupRestore') }}
                        </el-button>
                        <el-button text size="small" @click="openBackupAction('download', row)">
                          {{ t('settings.configBackupDownload') }}
                        </el-button>
                        <el-button text type="danger" size="small" @click="openBackupAction('delete', row)">
                          {{ t('common.delete') }}
                        </el-button>
                      </template>
                    </el-table-column>
                    <template #empty>
                      <div style="padding: 28px 0; color: #94a3b8">{{ t('settings.configBackupEmpty') }}</div>
                    </template>
                  </el-table>
                </div>
              </div>
            </div>
          </div>

          <div v-if="configReady && configForm" class="settings-footer">
            <div class="footer-actions">
              <div class="footer-action-group">
                <el-button type="primary" :disabled="!configWritable" :loading="configSaving" @click="openConfigSave">
                  {{ t('settings.configSave') }}
                </el-button>
              </div>
              <div class="footer-action-group">
                <el-button :disabled="!configWritable" @click="openConfigBackup">
                  {{ t('settings.configBackupNow') }}
                </el-button>
              </div>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- TLS 信任链 -->
      <el-tab-pane :label="t('settings.tabTrust')" name="trust">
        <div class="k-card settings-card">
          <div class="card-head">
            <div>
              <h3>{{ t('settings.trustTitle') }}</h3>
              <div class="sub">{{ t('settings.trustSub') }}</div>
            </div>
          </div>

          <div class="settings-scroll trust-scroll">
            <el-alert
              type="warning"
              :closable="false"
              show-icon
              :title="t('settings.trustAlert')"
              style="margin-bottom: 16px"
            />

            <div class="trust-list" :class="{ 'is-empty': !trustedCAs.length }" v-loading="trustLoading">
              <div v-if="!trustedCAs.length" class="trust-empty">
                <el-icon size="28"><Lock /></el-icon>
                <p>{{ t('settings.trustEmpty') }}</p>
              </div>
              <div v-for="row in trustedCAs" :key="row.id" class="trust-item">
                <div class="trust-head">
                  <div class="trust-name">
                    <el-icon><Lock /></el-icon>
                    <span>{{ row.name }}</span>
                  </div>
                  <el-button text type="danger" size="small" @click="removeTrustedCA(row)">
                    {{ t('common.delete') }}
                  </el-button>
                </div>
                <div class="trust-subject mono">{{ row.subject }}</div>
                <div class="trust-meta">
                  <div class="trust-field">
                    <span class="trust-label">{{ t('settings.trustIssuer') }}</span>
                    <span class="trust-value mono">{{ row.issuer }}</span>
                  </div>
                  <div class="trust-field">
                    <span class="trust-label">{{ t('settings.trustExpires') }}</span>
                    <span class="trust-value">{{ formatDate(row.not_after) }}</span>
                  </div>
                </div>
                <div class="trust-field trust-fingerprint">
                  <span class="trust-label">{{ t('settings.trustFingerprint') }}</span>
                  <span class="trust-value mono">{{ row.fingerprint }}</span>
                </div>
              </div>
            </div>
            <div class="form-help" style="margin-top: 12px">{{ t('settings.trustBuiltinNote') }}</div>
          </div>

          <div class="settings-footer">
            <div class="footer-actions">
              <div class="footer-action-group">
                <el-button :icon="Download" @click="downloadTrustBundle">{{ t('settings.trustDownload') }}</el-button>
              </div>
              <div class="footer-action-group">
                <el-button type="primary" :icon="Upload" @click="openTrustDialog">{{ t('settings.trustImport') }}</el-button>
              </div>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- 关于 -->
      <el-tab-pane :label="t('settings.tabAbout')" name="about">
        <div class="k-card settings-card">
          <div class="card-head">
            <div class="about-head">
              <img src="/logo.svg" alt="LACTSSL" style="width: 46px; height: 46px" />
              <div>
                <h3>LACTSSL</h3>
                <div class="sub">{{ t('settings.aboutDesc') }}</div>
              </div>
            </div>
            <span class="tag-soft tag-gray">{{ meta?.version || '1.0.0' }}</span>
          </div>

          <div class="settings-scroll">
            <div class="about-build muted">
              <span>{{ t('settings.buildVersion') }}</span>
              <span class="mono">{{ meta?.version || '1.0.0' }}</span>
              <span>{{ t('settings.buildTime') }}</span>
              <span class="mono">{{ formatDate(status?.build_time) }}</span>
              <span>{{ t('settings.buildArch') }}</span>
              <span class="mono">{{ status?.build_arch || t('common.none') }}</span>
            </div>

            <div class="grid grid-2 about-grid">
              <div class="about-panel">
                <div class="card-head"><h3>{{ t('settings.features') }}</h3></div>
                <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature1') }}</div>
                <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature2') }}</div>
                <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature3') }}</div>
                <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature4') }}</div>
                <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature5') }}</div>
              </div>
              <div class="about-panel">
                <div class="card-head"><h3>{{ t('settings.techStack') }}</h3></div>
                <div class="about-line"><span>{{ t('settings.backend') }}</span><span class="mono">Go + Gin + SQLite</span></div>
                <div class="about-line"><span>{{ t('settings.frontend') }}</span><span class="mono">Vue 3 + Vite + Element Plus</span></div>
                <div class="about-line"><span>{{ t('settings.acme') }}</span><span class="mono">lego / RFC 8555</span></div>
                <div class="about-line"><span>{{ t('settings.storage') }}</span><span class="mono">SQLite (encrypted)</span></div>
                <div class="about-line"><span>{{ t('settings.repo') }}</span><a href="https://github.com/AXmishell/LACTSSL" target="_blank" rel="noreferrer">github.com/AXmishell/LACTSSL</a></div>
              </div>
            </div>

            <div class="settings-section">
              <div class="section-head">
                <h3>{{ t('settings.systemInfo') }}</h3>
              </div>
              <div class="about-table-wrap">
                <table class="about-table about-table-h">
                  <thead>
                    <tr>
                      <th scope="col">{{ t('common.status') }}</th>
                      <th scope="col">{{ t('settings.uptime') }}</th>
                      <th scope="col">{{ t('settings.startedAt') }}</th>
                      <th scope="col">{{ t('settings.goroutines') }}</th>
                      <th scope="col">{{ t('settings.threads') }}</th>
                      <th scope="col">{{ t('settings.memory') }}</th>
                      <th scope="col">{{ t('settings.vmRss') }}</th>
                      <th scope="col">{{ t('settings.rssAnon') }}</th>
                      <th scope="col">{{ t('settings.vmSwap') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>
                        <span
                          class="tag-soft"
                          :class="status ? 'tag-green' : statusError ? 'tag-red' : 'tag-gray'"
                          :title="statusError || undefined"
                        >
                          {{ status ? t('settings.running') : statusError ? t('settings.statusError') : t('settings.loading') }}
                        </span>
                      </td>
                      <td class="mono">{{ uptimeText }}</td>
                      <td class="mono">{{ status ? formatDate(status.started_at) : t('common.none') }}</td>
                      <td class="mono">{{ status?.goroutines ?? t('common.none') }}</td>
                      <td class="mono">{{ status?.threads ?? t('common.none') }}</td>
                      <td class="mono">{{ memoryText }}</td>
                      <td class="mono">{{ formatBytes(status?.memory_rss) }}</td>
                      <td class="mono">{{ formatBytes(status?.memory_anon) }}</td>
                      <td class="mono">{{ formatBytes(status?.memory_swap) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>

    <!-- 最近安全事件 -->
    <el-dialog
      v-model="securityEventsVisible"
      :title="t('notifications.securityEventsTitle')"
      width="min(1040px, 94vw)"
      top="6vh"
    >
      <div class="security-events-toolbar">
        <el-button :icon="Refresh" circle :loading="securityEventsLoading" @click="loadSecurityEvents" />
        <el-button text @click="exportSecurityEvents">{{ t('notifications.securityEventsExport') }}</el-button>
      </div>
      <el-table :data="securityEvents" v-loading="securityEventsLoading" max-height="520" size="small">
        <el-table-column :label="t('notifications.securityEventsTime')" width="150">
          <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
        </el-table-column>
        <el-table-column :label="t('notifications.securityEventsKind')" min-width="150">
          <template #default="{ row }">{{ securityEventLabel(row.kind) }}</template>
        </el-table-column>
        <el-table-column :label="t('notifications.securityEventsStatus')" width="120">
          <template #default="{ row }">
            <el-tag :type="securityEventStatus(row).type" size="small">
              {{ securityEventStatus(row).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('notifications.securityEventsTargets')" min-width="240">
          <template #default="{ row }">
            <div v-for="target in securityEventTargets(row)" :key="target.key" class="security-target">
              <span class="mono">{{ target.label }}</span>
              <span class="muted">{{ target.state }}</span>
            </div>
            <span v-if="!securityEventTargets(row).length" class="muted">{{ t('common.none') }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('notifications.securityEventsError')" min-width="200">
          <template #default="{ row }">
            <span v-if="row.last_error" class="security-error">{{ row.last_error }}</span>
            <span v-else class="muted">{{ t('common.none') }}</span>
          </template>
        </el-table-column>
      </el-table>
      <template #footer>
        <el-button @click="securityEventsVisible = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>

    <!-- 修改邮箱 -->
    <el-dialog v-model="emailVisible" :title="t('settings.changeEmailTitle')" width="440px">
      <el-form label-position="top">
        <el-form-item :label="t('settings.newEmail')">
          <el-input v-model="emailForm.email" placeholder="name@example.com" />
        </el-form-item>
        <ReauthFields
          v-model:password="emailForm.password"
          v-model:totp="emailForm.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="emailVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="savingAccount" @click="saveEmail">{{ t('settings.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 修改密码 -->
    <el-dialog v-model="passwordVisible" :title="t('settings.changePasswordTitle')" width="440px">
      <el-form label-position="top">
        <ReauthFields
          v-model:password="passwordForm.password"
          v-model:totp="passwordForm.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
        <el-form-item :label="t('settings.newPassword')">
          <el-input v-model="passwordForm.new_password" type="password" show-password :placeholder="t('settings.passwordMinPlaceholder')" />
        </el-form-item>
        <el-form-item :label="t('settings.confirmNewPassword')">
          <el-input v-model="passwordForm.confirm" type="password" show-password />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="passwordVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="savingAccount" @click="savePassword">{{ t('settings.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 启用 TOTP -->
    <el-dialog v-model="totpVisible" :title="t('settings.enableTotpTitle')" width="460px" @closed="totpSetup = null">
      <div v-loading="totpLoading">
        <p class="muted" style="margin-top: 0">{{ t('settings.totpDesc') }}</p>
        <el-form label-position="top">
          <ReauthFields
            v-model:password="totpReauth.password"
            v-model:totp="totpReauth.totp"
            :totp-enabled="!!user?.totp_enabled"
          />
        </el-form>
        <template v-if="totpSetup">
          <div style="text-align: center; padding: 8px 0 16px">
            <img v-if="totpQr" :src="totpQr" alt="TOTP QR" style="width: 190px; height: 190px; border-radius: 12px; border: 1px solid #eef1f6" />
          </div>
          <div class="totp-secret">
            <span class="mono">{{ totpSetup?.secret }}</span>
            <el-button text type="primary" size="small" @click="copySecret">{{ t('settings.copy') }}</el-button>
          </div>
          <el-form label-position="top" style="margin-top: 18px">
            <el-form-item :label="t('settings.totpCodeLabel')">
              <el-input v-model="totpCode" maxlength="6" :placeholder="t('settings.totpCodePlaceholder')" @keyup.enter="enableTOTP" />
            </el-form-item>
          </el-form>
        </template>
        <div v-else class="form-help">{{ t('settings.totpLoadHint') }}</div>
      </div>
      <template #footer>
        <el-button @click="totpVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button v-if="!totpSetup" type="primary" :loading="totpLoading" @click="loadTOTPSecret">{{ t('settings.totpLoadSecret') }}</el-button>
        <el-button v-else type="primary" :loading="savingAccount" @click="enableTOTP">{{ t('settings.confirmEnable') }}</el-button>
      </template>
    </el-dialog>

    <!-- 关闭 TOTP -->
    <el-dialog v-model="disableVisible" :title="t('settings.disableTotpTitle')" width="420px">
      <el-form label-position="top">
        <ReauthFields
          v-model:password="totpDisableForm.password"
          v-model:totp="totpDisableForm.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="disableVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="danger" :loading="savingAccount" @click="disableTOTP">{{ t('settings.confirmDisable') }}</el-button>
      </template>
    </el-dialog>

    <!-- 添加通行密钥 -->
    <el-dialog v-model="passkeyVisible" :title="t('settings.addPasskeyTitle')" width="460px">
      <p class="muted" style="margin-top: 0">{{ t('settings.addPasskeyDesc') }}</p>
      <el-form label-position="top">
        <el-form-item :label="t('settings.passkeyName')">
          <el-input v-model="passkeyForm.name" maxlength="64" :placeholder="t('settings.passkeyNamePlaceholder')" />
        </el-form-item>
        <ReauthFields
          v-model:password="passkeyReauth.password"
          v-model:totp="passkeyReauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="passkeyVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="passkeyLoading" @click="addPasskey">{{ t('settings.addPasskey') }}</el-button>
      </template>
    </el-dialog>

    <!-- 删除通行密钥 -->
    <el-dialog v-model="passkeyDeleteVisible" :title="t('settings.deletePasskeyTitle')" width="420px">
      <p class="muted" style="margin-top: 0">
        {{ t('settings.deletePasskeyDesc', { name: passkeyDeleteTarget?.name || '' }) }}
      </p>
      <el-form label-position="top">
        <ReauthFields
          v-model:password="passkeyReauth.password"
          v-model:totp="passkeyReauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="passkeyDeleteVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="danger" :loading="passkeyLoading" @click="deletePasskey">{{ t('common.delete') }}</el-button>
      </template>
    </el-dialog>

    <!-- 下载备份 -->
    <el-dialog v-model="backupDialogVisible" :title="t('settings.backupDialogTitle')" width="460px" @closed="resetBackupDialog">
      <p class="muted" style="margin-top: 0">{{ t('settings.backupPasswordHint') }}</p>
      <el-form label-position="top">
        <el-form-item :label="t('settings.backupPassword')">
          <el-input
            v-model="backupPassword"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="t('settings.backupPasswordPlaceholder')"
          />
        </el-form-item>
        <el-form-item v-if="backupPassword" :label="t('settings.backupPasswordConfirm')">
          <el-input v-model="backupPasswordConfirm" type="password" show-password autocomplete="new-password" />
        </el-form-item>
        <ReauthFields
          v-model:password="backupReauth.password"
          v-model:totp="backupReauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <el-alert
        type="info"
        :closable="false"
        show-icon
        :title="t('settings.backupPasswordKeep')"
        style="margin-top: 4px"
      />
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        :title="t('settings.backupSecurityHint')"
        style="margin-top: 8px"
      />
      <template #footer>
        <el-button @click="backupDialogVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="backupDownloading" @click="downloadBackup">
          {{ t('settings.backupDownload') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 恢复备份 -->
    <el-dialog v-model="restoreVisible" :title="t('settings.restoreDialogTitle')" width="520px" @closed="resetRestore">
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        :title="t('settings.restoreWarning')"
        style="margin-bottom: 16px"
      />
      <el-upload
        ref="restoreUpload"
        drag
        :auto-upload="false"
        :show-file-list="false"
        accept=".zip,.db"
        :on-change="onRestoreFileChange"
      >
        <el-icon class="el-icon--upload"><UploadFilled /></el-icon>
        <div class="el-upload__text">{{ t('settings.restoreDrop') }}</div>
        <template #tip>
          <div class="el-upload__tip">{{ t('settings.restoreFileTip') }}</div>
        </template>
      </el-upload>
      <div v-if="restoreFileName" class="restore-file">
        <el-icon><Document /></el-icon>
        <span class="mono">{{ t('settings.restoreSelected') }}</span>
        <span class="restore-file-name">{{ restoreFileName }}</span>
      </div>
      <el-form label-position="top" style="margin-top: 16px">
        <el-form-item :label="t('settings.restoreDecryptPassword')">
          <el-input
            v-model="restoreBackupPassword"
            type="password"
            show-password
            autocomplete="off"
            :placeholder="t('settings.restoreDecryptPlaceholder')"
          />
          <div class="form-help">{{ t('settings.restoreDecryptHint') }}</div>
        </el-form-item>
        <ReauthFields
          v-model:password="restoreReauth.password"
          v-model:totp="restoreReauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="restoreVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button
          type="danger"
          :loading="restoring"
          :disabled="!restoreFile"
          @click="restoreBackup"
        >
          {{ t('settings.confirmRestore') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 保存配置文件 -->
    <el-dialog v-model="configSaveVisible" :title="t('settings.configSaveTitle')" width="480px" @closed="resetConfigSave">
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        :title="t('settings.configSaveWarning')"
        style="margin-bottom: 16px"
      />
      <el-form label-position="top">
        <el-form-item :label="t('settings.configNote')">
          <el-input v-model="configSaveNote" maxlength="200" :placeholder="t('settings.configNotePlaceholder')" />
        </el-form-item>
        <ReauthFields
          v-model:password="configSaveReauth.password"
          v-model:totp="configSaveReauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="configSaveVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="configSaving" @click="saveConfig">{{ t('settings.configSave') }}</el-button>
      </template>
    </el-dialog>

    <!-- 手动备份配置文件 -->
    <el-dialog v-model="configBackupVisible" :title="t('settings.configBackupNow')" width="460px" @closed="resetConfigBackup">
      <p class="muted" style="margin-top: 0">{{ t('settings.configBackupCreateHint') }}</p>
      <el-form label-position="top">
        <el-form-item :label="t('settings.configNote')">
          <el-input v-model="configBackupNote" maxlength="200" :placeholder="t('settings.configNotePlaceholder')" />
        </el-form-item>
        <ReauthFields
          v-model:password="configBackupReauth.password"
          v-model:totp="configBackupReauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="configBackupVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="configBackupSaving" @click="createConfigBackup">
          {{ t('settings.configBackupNow') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 配置文件快照操作 -->
    <el-dialog v-model="backupActionVisible" :title="backupActionTitle" width="460px" @closed="resetBackupAction">
      <el-alert
        v-if="backupAction === 'download'"
        type="warning"
        :closable="false"
        show-icon
        :title="t('settings.configBackupDownloadWarning')"
        style="margin-bottom: 16px"
      />
      <el-alert
        v-else-if="backupAction === 'restore'"
        type="warning"
        :closable="false"
        show-icon
        :title="t('settings.configBackupRestoreWarning')"
        style="margin-bottom: 16px"
      />
      <el-alert
        v-else
        type="error"
        :closable="false"
        show-icon
        :title="t('settings.configBackupDeleteWarning')"
        style="margin-bottom: 16px"
      />
      <div v-if="backupTarget" class="muted" style="margin-bottom: 12px">
        <span class="mono">{{ backupTarget.id }}</span>
        <span> · {{ formatDate(backupTarget.created_at) }}</span>
        <span v-if="backupTarget.note"> · {{ backupTarget.note }}</span>
      </div>
      <el-form label-position="top">
        <ReauthFields
          v-model:password="backupActionReauth.password"
          v-model:totp="backupActionReauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="backupActionVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button
          :type="backupAction === 'download' ? 'primary' : 'danger'"
          :loading="backupActionLoading"
          @click="confirmBackupAction"
        >
          {{ backupActionConfirmText }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 自动检测反向代理 -->
    <el-dialog v-model="proxyVisible" :title="t('settings.configDetectTitle')" width="620px">
      <div v-loading="proxyLoading">
        <el-alert
          v-for="warning in proxyHints?.warnings || []"
          :key="warning.code + (warning.detail || '')"
          type="warning"
          :closable="false"
          show-icon
          :title="proxyWarningText(warning)"
          style="margin-bottom: 12px"
        />
        <div v-if="proxyHints" class="proxy-info">
          <div class="about-line">
            <span>{{ t('settings.configDetectPeer') }}</span>
            <span class="mono">{{ proxyHints.peer_ip || t('common.none') }}</span>
          </div>
          <div class="about-line">
            <span>{{ t('settings.configDetectEffective') }}</span>
            <span class="mono">{{ proxyHints.effective_client_ip || t('common.none') }}</span>
          </div>
          <div class="about-line">
            <span>{{ t('settings.configDetectSource') }}</span>
            <span class="mono">{{ proxyHints.client_ip_source || t('common.none') }}</span>
          </div>
          <div class="about-line">
            <span>{{ t('settings.configDetectApplied') }}</span>
            <span class="mono" style="word-break: break-all">{{ (proxyHints.applied_trusted_proxies || []).join(', ') || t('common.none') }}</span>
          </div>
          <div class="about-line">
            <span>{{ t('settings.configDetectConfigured') }}</span>
            <span class="mono" style="word-break: break-all">{{ (proxyHints.configured_trusted_proxies || []).join(', ') || t('common.none') }}</span>
          </div>
          <div class="about-line">
            <span>{{ t('settings.configDetectForwarded') }}</span>
            <span class="mono" style="word-break: break-all">{{ forwardedText }}</span>
          </div>
          <div v-if="proxyHints.header_warnings?.length" class="about-line">
            <span>{{ t('settings.configDetectHeaderWarnings') }}</span>
            <span class="mono" style="word-break: break-all">{{ headerWarningText(proxyHints.header_warnings) }}</span>
          </div>
        </div>
        <div v-if="proxyHints?.suggestions?.length" class="proxy-suggestions">
          <div v-for="item in proxyHints.suggestions" :key="item.value" class="proxy-suggestion">
            <div style="min-width: 0">
              <div class="mono" style="font-weight: 600">{{ item.value }}</div>
              <div class="muted" style="margin-top: 4px; font-size: 12px">
                {{ proxyReasonText(item.reason) }}
              </div>
              <el-checkbox
                v-if="item.requires_confirmation && !item.already_trusted"
                v-model="proxyConfirmations[item.value]"
                style="margin-top: 6px; height: auto; white-space: normal"
              >
                {{ t('settings.configDetectConfirm') }}
              </el-checkbox>
            </div>
            <div class="flex gap-10" style="align-items: center">
              <span class="tag-soft" :class="item.confidence === 'high' ? 'tag-green' : 'tag-gray'">
                {{ proxyConfidenceText(item.confidence) }}
              </span>
              <el-button
                size="small"
                type="primary"
                plain
                :disabled="!canAddProxy(item)"
                @click="addProxySuggestion(item.value)"
              >
                {{ item.already_trusted ? t('settings.configDetectAlready') : t('settings.configDetectAdd') }}
              </el-button>
            </div>
          </div>
        </div>
        <div v-else-if="!proxyLoading" class="muted" style="padding: 16px 0">
          {{ t('settings.configDetectEmpty') }}
        </div>
      </div>
      <template #footer>
        <el-button @click="proxyVisible = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>

    <!-- 重启以应用配置 -->
    <el-dialog v-model="configRestartVisible" :title="t('settings.configRestartTitle')" width="540px" @closed="resetConfigRestart">
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        :title="t('settings.configRestartWarning')"
        style="margin-bottom: 16px"
      />
      <div class="restart-info">
        <div class="about-line">
          <span>{{ t('settings.configEffectiveAddr') }}</span>
          <span class="mono">{{ configEffectiveAddr || t('common.none') }}</span>
        </div>
        <div class="about-line">
          <span>{{ t('settings.configRestartSession') }}</span>
          <span>{{ t('settings.configRestartSessionHint') }}</span>
        </div>
      </div>
      <el-form label-position="top">
        <ReauthFields
          v-model:password="configRestartReauth.password"
          v-model:totp="configRestartReauth.totp"
          :totp-enabled="!!user?.totp_enabled"
        />
      </el-form>
      <template #footer>
        <el-button @click="configRestartVisible = false">{{ t('settings.configRestartLater') }}</el-button>
        <el-button type="warning" :loading="configRestarting" @click="restartConfigService">
          {{ t('settings.configRestartNow') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 导入内部 CA -->
    <el-dialog v-model="trustVisible" :title="t('settings.trustImportTitle')" width="640px" destroy-on-close>
      <p class="muted" style="margin-top: 0">{{ t('settings.trustImportHint') }}</p>
      <el-form label-position="top">
        <el-form-item :label="t('settings.trustName')">
          <el-input v-model="trustForm.name" maxlength="128" :placeholder="t('settings.trustNamePlaceholder')" />
        </el-form-item>
        <el-form-item :label="t('settings.trustPEM')" required>
          <div class="flex gap-10 wrap" style="margin-bottom: 8px; width: 100%">
            <el-upload
              :auto-upload="false"
              :show-file-list="false"
              accept=".pem,.crt,.cer,.txt"
              :on-change="onTrustFileChange"
            >
              <el-button :icon="Upload">{{ t('settings.trustChooseFile') }}</el-button>
            </el-upload>
            <span class="muted" style="font-size: 12px; align-self: center">{{ t('settings.trustFileHint') }}</span>
          </div>
          <el-input
            v-model="trustForm.cert_pem"
            type="textarea"
            :rows="10"
            class="mono"
            :placeholder="t('settings.trustPEMPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="trustVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="trustSaving" @click="importTrustedCA">{{ t('settings.trustImport') }}</el-button>
      </template>
    </el-dialog>
    <el-dialog v-model="entryReauthVisible" :title="t('settings.updateEntry')" width="420px" destroy-on-close>
      <ReauthFields
        v-model:password="entryReauth.password"
        v-model:totp="entryReauth.totp"
        :totp-enabled="!!user?.totp_enabled"
      />
      <template #footer>
        <el-button @click="entryReauthVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="savingEntry" @click="confirmSaveEntry">{{ t('settings.updateEntry') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UploadFile, UploadInstance } from 'element-plus'
import { Document, Download, Lock, Refresh, Upload, UploadFilled } from '@element-plus/icons-vue'
import QRCode from 'qrcode'
import ReauthFields from '../components/ReauthFields.vue'
import ConfigEditor from '../components/ConfigEditor.vue'
import { api, copyText, downloadFile, errorText } from '../api'
import { isPasskeyCancelled, passkeySecureContext, passkeySupported, registerPasskey } from '../passkeys'
import { notifyChannelLabelKey } from '../notifyChannels'
import { loadMeta, loadSettings, loadUser, logout, state } from '../store'
import { formatDate } from '../utils'
import type {
  AuthResult,
  BackupInfo,
  ConfigBackupInfo,
  ConfigProxyHints,
  ConfigProxySuggestion,
  ConfigProxyWarning,
  MessageResult,
  Meta,
  PasskeyInfo,
  PendingConfigRestart,
  SecurityEventView,
  Settings as AppSettings,
  SettingsConfigResult,
  SettingsConfigSaveResult,
  SetupConfig,
  TOTPSetup,
  TrustedCA
} from '../types'

interface ProgramStatus {
  version: string
  build_time: string
  build_arch: string
  started_at: string
  uptime_seconds: number
  go_version: string
  goroutines: number
  threads: number
  memory_alloc: number
  memory_heap: number
  memory_sys: number
  memory_rss: number
  memory_anon: number
  memory_vsz: number
  memory_swap: number
}

const securityEventOptions = [
  { value: 'credential.password.changed', labelKey: 'notifications.securityEventPassword' },
  { value: 'credential.email.changed', labelKey: 'notifications.securityEventEmail' },
  { value: 'credential.totp.enabled', labelKey: 'notifications.securityEventTOTPEnabled' },
  { value: 'credential.totp.disabled', labelKey: 'notifications.securityEventTOTPDisabled' },
  { value: 'credential.passkey.added', labelKey: 'notifications.securityEventPasskeyAdded' },
  { value: 'credential.passkey.deleted', labelKey: 'notifications.securityEventPasskeyDeleted' },
  { value: 'credential.recovery.reset', labelKey: 'notifications.securityEventReset' },
  { value: 'apikey.created', labelKey: 'notifications.securityEventAPIKeyCreated' },
  { value: 'apikey.updated', labelKey: 'notifications.securityEventAPIKeyUpdated' },
  { value: 'apikey.deleted', labelKey: 'notifications.securityEventAPIKeyDeleted' },
  { value: 'apikey.rotated', labelKey: 'notifications.securityEventAPIKeyRotated' },
  { value: 'settings.notify.changed', labelKey: 'notifications.securityEventNotify' }
]

const defaultSecurityEvents = securityEventOptions.map((item) => item.value)

const { t } = useI18n()
const router = useRouter()
const tab = ref('security')
const meta = ref<Meta | null>(null)
const settings = ref<AppSettings | null>(null)
const status = ref<ProgramStatus | null>(null)
const statusError = ref('')
const statusAt = ref(0)
const now = ref(Date.now())
let statusTimer: number | undefined
let clockTimer: number | undefined
const user = computed(() => state.user)
const passkeys = ref<PasskeyInfo[]>([])
const passkeyLoading = ref(false)
const passkeyUsable = computed(() => passkeySupported() && passkeySecureContext())

const savingEntry = ref(false)
const savingAccount = ref(false)
const savingSecurity = ref(false)
const testingSecurity = ref(false)
const securityEventsVisible = ref(false)
const securityEventsLoading = ref(false)
const securityEvents = ref<SecurityEventView[]>([])
const security = reactive({
  enabled: true,
  events: [...defaultSecurityEvents] as string[],
  to: '',
  retention_days: 90,
  login: 'new' as 'off' | 'new' | 'always',
  failed_login: true,
  failed_threshold: 5,
  failed_window: 600,
  new_device_days: 30,
  channels: ['mail'] as string[],
  max_per_hour: 10,
  last_error: '',
  last_delivered_at: '' as string | null
})

const securityChannelOptions = computed(() => {
  const options = [{ value: 'mail', label: t('notifications.channelMail') }]
  for (const def of settings.value?.notify_channel_defs || []) {
    if (def.id === 'mail') continue
    const labelKey = notifyChannelLabelKey(def.id)
    options.push({
      value: def.id,
      label: def.name || (labelKey ? t(labelKey) : def.id)
    })
  }
  return options
})

const securityEventLabelKeys: Record<string, string> = {}
for (const item of securityEventOptions) {
  securityEventLabelKeys[item.value] = item.labelKey
}
securityEventLabelKeys['login.password'] = 'notifications.securityEventLoginPassword'
securityEventLabelKeys['login.passkey'] = 'notifications.securityEventLoginPasskey'
securityEventLabelKeys['login.failed'] = 'notifications.securityEventLoginFailed'
securityEventLabelKeys['security.test'] = 'notifications.securityEventTest'
securityEventLabelKeys['security.digest'] = 'notifications.securityEventDigest'

function securityEventLabel(kind: string): string {
  const key = securityEventLabelKeys[kind]
  return key ? t(key) : kind
}

function securityEventStatus(event: SecurityEventView): { label: string; type: 'success' | 'danger' | 'warning' | 'info' } {
  // 后端按逐目标 deliveries 计算状态；这里保留旧字段兜底，兼容缓存的旧响应。
  switch (event.status) {
    case 'recorded':
      return { label: t('notifications.securityEventRecorded'), type: 'info' }
    case 'delivered':
      return { label: t('notifications.securityEventDelivered'), type: 'success' }
    case 'failed':
      return { label: t('notifications.securityEventFailed'), type: 'danger' }
    case 'retrying':
      return { label: t('notifications.securityEventRetrying'), type: 'warning' }
    case 'pending':
      return { label: t('notifications.securityEventPending'), type: 'warning' }
  }
  if (!event.notify) {
    return { label: t('notifications.securityEventRecorded'), type: 'info' }
  }
  if (event.delivered_at) {
    return { label: t('notifications.securityEventDelivered'), type: 'success' }
  }
  if (event.last_error) {
    return { label: t('notifications.securityEventFailed'), type: 'danger' }
  }
  return { label: t('notifications.securityEventPending'), type: 'warning' }
}

function securityTargetState(state?: string): string {
  switch (state) {
    case 'sent':
      return t('notifications.securityEventSent')
    case 'failed':
      return t('notifications.securityEventTargetFailed')
    case 'skipped':
      return t('notifications.securityEventSkipped')
    default:
      return t('notifications.securityEventPending')
  }
}

function securityEventTargets(event: SecurityEventView): Array<{ key: string; label: string; state: string }> {
  if (!event.notify) return []
  const targets: Array<{ key: string; label: string; state: string }> = []
  for (const recipient of event.recipients || []) {
    const key = `mail:${recipient.toLowerCase()}`
    targets.push({ key, label: recipient, state: securityTargetState(event.deliveries?.[key]) })
  }
  for (const channel of event.channels || []) {
    if (channel === 'mail') continue
    const key = `channel:${channel.toLowerCase()}`
    const label = securityChannelOptions.value.find((item) => item.value === channel)?.label || channel
    targets.push({ key, label, state: securityTargetState(event.deliveries?.[key]) })
  }
  return targets
}

async function loadSecurityEvents(): Promise<void> {
  securityEventsLoading.value = true
  try {
    const data = await api.get<{ events: SecurityEventView[] }>('/security/events?limit=30')
    securityEvents.value = data?.events || []
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    securityEventsLoading.value = false
  }
}

function openSecurityEvents(): void {
  securityEventsVisible.value = true
  void loadSecurityEvents()
}

function exportSecurityEvents(): void {
  const blob = new Blob([JSON.stringify(securityEvents.value, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `lactssl-security-events-${new Date().toISOString().slice(0, 10)}.json`
  link.click()
  URL.revokeObjectURL(url)
}

const entryForm = reactive({ entry: '' })
const entryReauth = reactive({ password: '', totp: '' })
const entryReauthVisible = ref(false)

const emailVisible = ref(false)
const emailForm = reactive({ email: '', password: '', totp: '' })
const passwordVisible = ref(false)
const passwordForm = reactive({ new_password: '', confirm: '', password: '', totp: '' })

const totpVisible = ref(false)
const totpLoading = ref(false)
const totpSetup = ref<TOTPSetup | null>(null)
const totpQr = ref('')
const totpCode = ref('')
const totpReauth = reactive({ password: '', totp: '' })
const disableVisible = ref(false)
const totpDisableForm = reactive({ password: '', totp: '' })

const passkeyVisible = ref(false)
const passkeyForm = reactive({ name: '' })
const passkeyDeleteVisible = ref(false)
const passkeyDeleteTarget = ref<PasskeyInfo | null>(null)
const passkeyReauth = reactive({ password: '', totp: '' })

const backup = ref<BackupInfo | null>(null)
const backupDownloading = ref(false)
const backupDialogVisible = ref(false)
const backupPassword = ref('')
const backupPasswordConfirm = ref('')
const backupReauth = reactive({ password: '', totp: '' })
const restoreVisible = ref(false)
const restoreUpload = ref<UploadInstance>()
const restoreFile = ref<File | null>(null)
const restoreFileName = ref('')
const restoreBackupPassword = ref('')
const restoreReauth = reactive({ password: '', totp: '' })
const restoring = ref(false)
let restoreTimer: number | undefined

const configLoading = ref(false)
const configReady = ref(false)
const configLoadError = ref('')
const configForm = ref<SetupConfig | null>(null)
const configRevision = ref('')
const configWritable = ref(true)
const configEffectiveAddr = ref('')
const configPending = ref<PendingConfigRestart | null>(null)
const configBackups = ref<ConfigBackupInfo[]>([])

const configSaveVisible = ref(false)
const configSaving = ref(false)
const configSaveNote = ref('')
const configSaveReauth = reactive({ password: '', totp: '' })

const configBackupVisible = ref(false)
const configBackupSaving = ref(false)
const configBackupNote = ref('')
const configBackupReauth = reactive({ password: '', totp: '' })

const backupActionVisible = ref(false)
const backupAction = ref<'download' | 'restore' | 'delete'>('download')
const backupTarget = ref<ConfigBackupInfo | null>(null)
const backupActionLoading = ref(false)
const backupActionReauth = reactive({ password: '', totp: '' })

const proxyVisible = ref(false)
const proxyLoading = ref(false)
const proxyHints = ref<ConfigProxyHints | null>(null)
const proxyConfirmations = ref<Record<string, boolean>>({})

const configRestartVisible = ref(false)
const configRestarting = ref(false)
const configRestartReauth = reactive({ password: '', totp: '' })
let configRestartTimer: number | undefined

const trustedCAs = ref<TrustedCA[]>([])
const trustLoading = ref(false)
const trustVisible = ref(false)
const trustSaving = ref(false)
const trustForm = reactive({ name: '', cert_pem: '' })

const uptimeText = computed(() => {
  const base = status.value?.uptime_seconds || 0
  const extra = statusAt.value ? Math.floor((now.value - statusAt.value) / 1000) : 0
  const seconds = base + extra
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return t('status.durationDaysHours', { days, hours })
  if (hours > 0) return t('status.durationHoursMinutes', { hours, minutes })
  if (minutes > 0) return t('status.durationMinutes', { minutes })
  return t('status.durationSeconds', { seconds })
})

const memoryText = computed(() => {
  if (!status.value) return t('common.none')
  return `${formatBytes(status.value.memory_alloc)} / ${formatBytes(status.value.memory_heap)}`
})

function formatBytes(bytes?: number): string {
  if (!bytes || bytes < 0) return t('common.none')
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

const forwardedText = computed(() => {
  const hints = proxyHints.value
  if (!hints) return t('common.none')
  const parts: string[] = []
  if (hints.forwarded.x_forwarded_for.length) {
    parts.push(`X-Forwarded-For: ${hints.forwarded.x_forwarded_for.join(', ')}`)
  }
  if (hints.forwarded.x_real_ip) parts.push(`X-Real-IP: ${hints.forwarded.x_real_ip}`)
  if (hints.forwarded.x_forwarded_proto) parts.push(`X-Forwarded-Proto: ${hints.forwarded.x_forwarded_proto}`)
  if (hints.forwarded.forwarded) parts.push(`Forwarded: ${hints.forwarded.forwarded}`)
  if (hints.forwarded.cf_connecting_ip) parts.push(`CF-Connecting-IP: ${hints.forwarded.cf_connecting_ip}`)
  return parts.length ? parts.join(' · ') : t('common.none')
})

const backupActionTitle = computed(() => {
  switch (backupAction.value) {
    case 'restore':
      return t('settings.configBackupRestore')
    case 'delete':
      return t('settings.configBackupDelete')
    default:
      return t('settings.configBackupDownload')
  }
})

const backupActionConfirmText = computed(() => {
  switch (backupAction.value) {
    case 'restore':
      return t('settings.configBackupRestore')
    case 'delete':
      return t('common.delete')
    default:
      return t('settings.configBackupDownload')
  }
})

function configBackupReason(reason: string): string {
  switch (reason) {
    case 'manual':
      return t('settings.configBackupReasonManual')
    case 'before-restore':
      return t('settings.configBackupReasonBeforeRestore')
    default:
      return t('settings.configBackupReasonBeforeSave')
  }
}

function proxyConfidenceText(confidence: string): string {
  if (confidence === 'high') return t('settings.configDetectConfidenceHigh')
  if (confidence === 'medium') return t('settings.configDetectConfidenceMedium')
  return t('settings.configDetectConfidenceLow')
}

function proxyReasonText(reason: string): string {
  switch (reason) {
    case 'loopback':
      return t('settings.configDetectReasonLoopback')
    case 'forwarded_headers':
      return t('settings.configDetectReasonForwarded')
    default:
      return t('settings.configDetectReasonDirect')
  }
}

function proxyWarningText(warning: ConfigProxyWarning): string {
  switch (warning.code) {
    case 'direct_connection':
      return t('settings.configDetectDirectWarning')
    case 'cdn_detected':
      return t('settings.configDetectCDNWarning')
    case 'broad_proxy_range':
      return t('settings.configDetectBroadWarning', { value: warning.detail || '' })
    case 'unverified_forwarded_headers':
      return t('settings.configDetectUnverifiedWarning')
    case 'public_peer_forwarded':
      return t('settings.configDetectPublicPeerWarning')
    case 'configured_differs_from_applied':
      return t('settings.configDetectRestartDiffWarning')
    case 'trusted_config_invalid':
      return t('settings.configDetectInvalidConfigWarning')
    default:
      return warning.code
  }
}

function canAddProxy(item: ConfigProxySuggestion): boolean {
  if (item.already_trusted) return false
  return !item.requires_confirmation || !!proxyConfirmations.value[item.value]
}

function headerWarningText(warnings: string[]): string {
  return warnings
    .map((code) => {
      switch (code) {
        case 'x_forwarded_for_unusable':
        case 'x_forwarded_for_too_large':
          return t('settings.configDetectHeaderXFFUnusable')
        case 'x_real_ip_unusable':
        case 'x_real_ip_too_large':
          return t('settings.configDetectHeaderXRealIPUnusable')
        case 'cf_connecting_ip_unusable':
        case 'cf_connecting_ip_too_large':
          return t('settings.configDetectHeaderCFUnusable')
        case 'forwarded_unusable':
        case 'forwarded_too_large':
          return t('settings.configDetectHeaderForwardedUnusable')
        case 'headers_unusable':
          return t('settings.configDetectHeaderFallback')
        case 'invalid_remote_addr':
          return t('settings.configDetectHeaderInvalidRemote')
        default:
          return code
      }
    })
    .join('; ')
}

function requireConfigReauth(form: { password: string; totp: string }): boolean {
  if (user.value?.totp_enabled) {
    if (form.totp.trim()) return true
  } else if (form.password) {
    return true
  }
  ElMessage.warning(t('settings.configReauthRequired'))
  return false
}

async function loadConfig(): Promise<void> {
  configLoading.value = true
  try {
    const data = await api.get<SettingsConfigResult>('/settings/config')
    configForm.value = data.config
    configRevision.value = data.revision
    configWritable.value = data.writable
    configEffectiveAddr.value = data.effective_addr
    configPending.value = data.restart_pending
    configBackups.value = data.backups || []
    configReady.value = true
    configLoadError.value = ''
  } catch (error: any) {
    configLoadError.value = errorText(error)
    configReady.value = false
  } finally {
    configLoading.value = false
  }
}

function openConfigSave(): void {
  if (!configForm.value) return
  configSaveNote.value = ''
  configSaveReauth.password = ''
  configSaveReauth.totp = ''
  configSaveVisible.value = true
}

function resetConfigSave(): void {
  configSaveNote.value = ''
  configSaveReauth.password = ''
  configSaveReauth.totp = ''
}

async function saveConfig(): Promise<void> {
  if (!configForm.value) return
  if (!requireConfigReauth(configSaveReauth)) return
  configSaving.value = true
  try {
    const form = configForm.value
    const result = await api.put<SettingsConfigSaveResult>('/settings/config', {
      revision: configRevision.value,
      note: configSaveNote.value.trim(),
      password: configSaveReauth.password,
      totp: configSaveReauth.totp.trim(),
      config: {
        ...form,
        rate_limit: {
          max_keys: form.rate_limit.max_keys,
          rules: form.rate_limit.rules
        }
      }
    })
    configSaveVisible.value = false
    ElMessage.success(result.message || t('settings.configSaved'))
    await loadConfig()
  } catch (error: any) {
    ElMessage.error(errorText(error))
  } finally {
    configSaving.value = false
  }
}

function openConfigBackup(): void {
  configBackupNote.value = ''
  configBackupReauth.password = ''
  configBackupReauth.totp = ''
  configBackupVisible.value = true
}

function resetConfigBackup(): void {
  configBackupNote.value = ''
  configBackupReauth.password = ''
  configBackupReauth.totp = ''
}

async function createConfigBackup(): Promise<void> {
  if (!requireConfigReauth(configBackupReauth)) return
  configBackupSaving.value = true
  try {
    await api.post<ConfigBackupInfo>('/settings/config/backups', {
      note: configBackupNote.value.trim(),
      password: configBackupReauth.password,
      totp: configBackupReauth.totp.trim()
    })
    configBackupVisible.value = false
    ElMessage.success(t('settings.configBackupCreated'))
    await loadConfig()
  } catch (error: any) {
    ElMessage.error(errorText(error))
  } finally {
    configBackupSaving.value = false
  }
}

function openBackupAction(action: 'download' | 'restore' | 'delete', row: ConfigBackupInfo): void {
  backupAction.value = action
  backupTarget.value = row
  backupActionReauth.password = ''
  backupActionReauth.totp = ''
  backupActionVisible.value = true
}

function resetBackupAction(): void {
  backupTarget.value = null
  backupActionReauth.password = ''
  backupActionReauth.totp = ''
  backupActionLoading.value = false
}

async function confirmBackupAction(): Promise<void> {
  const target = backupTarget.value
  if (!target) return
  if (!requireConfigReauth(backupActionReauth)) return
  backupActionLoading.value = true
  try {
    if (backupAction.value === 'download') {
      await downloadFile(`/settings/config/backups/${target.id}/download`, `config-${target.id}.yaml`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          password: backupActionReauth.password,
          totp: backupActionReauth.totp.trim()
        })
      })
      ElMessage.success(t('settings.configBackupDownloaded'))
    } else if (backupAction.value === 'restore') {
      const result = await api.post<SettingsConfigSaveResult>(
        `/settings/config/backups/${target.id}/restore`,
        { password: backupActionReauth.password, totp: backupActionReauth.totp.trim() }
      )
      ElMessage.success(result.message || t('settings.configBackupRestored'))
      await loadConfig()
    } else {
      await api.deleteWithBody<MessageResult>(`/settings/config/backups/${target.id}`, {
        password: backupActionReauth.password,
        totp: backupActionReauth.totp.trim()
      })
      ElMessage.success(t('settings.configBackupDeletedMessage'))
      await loadConfig()
    }
    backupActionVisible.value = false
  } catch (error: any) {
    ElMessage.error(errorText(error))
  } finally {
    backupActionLoading.value = false
  }
}

function openProxyDialog(): void {
  proxyVisible.value = true
  proxyConfirmations.value = {}
  loadProxyHints()
}

async function loadProxyHints(): Promise<void> {
  proxyLoading.value = true
  try {
    proxyHints.value = await api.get<ConfigProxyHints>('/settings/config/proxy-hints')
  } catch (error: any) {
    proxyHints.value = null
    ElMessage.error(errorText(error))
  } finally {
    proxyLoading.value = false
  }
}

function addProxySuggestion(value: string): void {
  if (!configForm.value) return
  if (configForm.value.trusted_proxies.includes(value)) {
    ElMessage.info(t('settings.configDetectAlready'))
    return
  }
  configForm.value.trusted_proxies.push(value)
  ElMessage.success(t('settings.configDetectAdded', { value }))
}

function openConfigRestart(): void {
  configRestartReauth.password = ''
  configRestartReauth.totp = ''
  configRestartVisible.value = true
}

function resetConfigRestart(): void {
  configRestartReauth.password = ''
  configRestartReauth.totp = ''
}

function configRestartURL(): string {
  const current = window.location
  let port = current.port
  const match = configEffectiveAddr.value.trim().match(/:(\d+)$/)
  if (match) port = match[1]
  const defaultPort = current.protocol === 'https:' ? '443' : '80'
  const portPart = port && port !== defaultPort ? `:${port}` : ''
  return `${current.protocol}//${current.hostname}${portPart}${current.pathname}${current.search}`
}

async function restartConfigService(): Promise<void> {
  if (!requireConfigReauth(configRestartReauth)) return
  if (configRestartTimer !== undefined) window.clearTimeout(configRestartTimer)
  configRestarting.value = true
  try {
    const result = await api.post<{ message?: string }>('/settings/config/restart', {
      revision: configRevision.value,
      password: configRestartReauth.password,
      totp: configRestartReauth.totp.trim()
    })
    ElMessage.success(result.message || t('settings.configRestarting'))
    configRestartVisible.value = false
    configRestartTimer = window.setTimeout(() => {
      window.location.replace(configRestartURL())
    }, 5000)
  } catch (error: any) {
    configRestarting.value = false
    ElMessage.error(errorText(error))
  }
}

function applySettings(data: AppSettings): void {
  settings.value = data
  // 默认填入当前入口，便于直接编辑。
  entryForm.entry = data.entry || ''

  const policy = data.security_notify
  Object.assign(security, {
    enabled: policy?.enabled !== false,
    events: Array.isArray(policy?.events) ? [...policy.events] : [...defaultSecurityEvents],
    // 默认引用系统账户邮箱填写；用户保存后即成为独立的收件人配置。
    to: (policy?.to || '').trim() || state.user?.email || '',
    retention_days: policy?.retention_days || 90,
    login: policy?.login === 'off' || policy?.login === 'always' ? policy.login : 'new',
    failed_login: policy?.failed_login !== false,
    failed_threshold: policy?.failed_threshold || 5,
    failed_window: policy?.failed_window || 600,
    new_device_days: policy?.new_device_days || 30,
    channels: Array.isArray(policy?.channels) && policy.channels.length ? [...policy.channels] : ['mail'],
    max_per_hour: policy?.max_per_hour || 10,
    last_error: policy?.last_error || '',
    last_delivered_at: policy?.last_delivered_at || null
  })
}

function goNotifications(): void {
  router.push('/notifications')
}

async function saveSecurity(): Promise<void> {
  savingSecurity.value = true
  try {
    const saved = await api.put('/settings', {
      security_notify: {
        enabled: security.enabled,
        events: [...security.events],
        to: security.to,
        retention_days: security.retention_days,
        login: security.login,
        failed_login: security.failed_login,
        failed_threshold: security.failed_threshold,
        failed_window: security.failed_window,
        new_device_days: security.new_device_days,
        channels: [...security.channels],
        max_per_hour: security.max_per_hour
      }
    })
    if (saved === null) return
    ElMessage.success(t('notifications.securitySaved'))
    applySettings(await loadSettings(true))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingSecurity.value = false
  }
}

async function sendSecurityTest(): Promise<void> {
  if (testingSecurity.value) return
  testingSecurity.value = true
  try {
    const data = await api.post<MessageResult>('/settings/test-security-notify', {
      channels: [...security.channels],
      to: security.to
    })
    if (data === null) return
    ElMessage.success(data.message || t('notifications.securityTestQueued'))
    applySettings(await loadSettings(true))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    testingSecurity.value = false
  }
}

async function loadStatus(): Promise<void> {
  try {
    status.value = await api.get<ProgramStatus>('/status')
    statusAt.value = Date.now()
    now.value = Date.now()
    statusError.value = ''
  } catch (error: any) {
    status.value = null
    statusAt.value = 0
    statusError.value = error?.message || t('settings.statusUnavailable')
    console.warn('failed to load status', error)
  }
}

async function loadBackupInfo(): Promise<void> {
  try {
    backup.value = await api.get<BackupInfo>('/settings/backup')
  } catch (error) {
    backup.value = null
    console.warn('failed to load backup info', error)
  }
}

async function loadPasskeys(): Promise<void> {
  try {
    passkeys.value = await api.get<PasskeyInfo[]>('/auth/passkeys')
  } catch (error) {
    passkeys.value = []
    console.warn('failed to load passkeys', error)
  }
}

async function loadTrustedCAs(): Promise<void> {
  trustLoading.value = true
  try {
    trustedCAs.value = await api.get<TrustedCA[]>('/trusted-cas')
  } catch (error) {
    trustedCAs.value = []
    console.warn('failed to load trusted CAs', error)
  } finally {
    trustLoading.value = false
  }
}

function openTrustDialog(): void {
  trustForm.name = ''
  trustForm.cert_pem = ''
  trustVisible.value = true
}

async function onTrustFileChange(file: UploadFile): Promise<void> {
  const raw = file.raw
  if (!raw) return
  if (raw.size > 1024 * 1024) {
    ElMessage.warning(t('settings.trustFileTooLarge'))
    return
  }
  try {
    trustForm.cert_pem = await raw.text()
  } catch (_) {
    ElMessage.error(t('settings.trustFileReadFailed'))
  }
}

async function importTrustedCA(): Promise<void> {
  if (!trustForm.cert_pem.trim()) {
    ElMessage.warning(t('settings.trustPEMRequired'))
    return
  }
  trustSaving.value = true
  try {
    const result = await api.post<{ message?: string; count?: number }>('/trusted-cas/import', {
      name: trustForm.name.trim(),
      cert_pem: trustForm.cert_pem
    })
    trustVisible.value = false
    await loadTrustedCAs()
    ElMessage.success(result?.message || t('settings.trustImported'))
  } catch (error: any) {
    ElMessage.error(errorText(error))
  } finally {
    trustSaving.value = false
  }
}

async function removeTrustedCA(item: TrustedCA): Promise<void> {
  try {
    await ElMessageBox.confirm(t('settings.trustDeleteConfirm', { name: item.name }), t('settings.trustDeleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('settings.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    await api.delete(`/trusted-cas/${item.id}`)
    await loadTrustedCAs()
    ElMessage.success(t('settings.trustDeleted'))
  } catch (error: any) {
    ElMessage.error(errorText(error))
  }
}

async function downloadTrustBundle(): Promise<void> {
  try {
    await downloadFile('/trusted-cas/bundle', 'lactssl-trusted-ca-bundle.pem')
  } catch (error: any) {
    ElMessage.error(errorText(error))
  }
}

async function reload(): Promise<void> {
  try {
    const [metaData, settingData] = await Promise.all([loadMeta(true), loadSettings(true), loadUser(true)])
    meta.value = metaData
    applySettings(settingData)
    await Promise.all([loadStatus(), loadBackupInfo(), loadPasskeys(), loadTrustedCAs(), loadConfig()])
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function saveEntry(): Promise<void> {
  const entry = entryForm.entry.trim().replace(/^\/+|\/+$/g, '')
  entryForm.entry = entry
  if (!/^[a-zA-Z0-9_-]{8,32}$/.test(entry)) {
    ElMessage.warning(t('settings.entryFormat'))
    return
  }
  if (['api', 'assets', 'healthz', 'static'].includes(entry.toLowerCase())) {
    ElMessage.warning(t('settings.entryReserved', { entry }))
    return
  }
  try {
    await ElMessageBox.confirm(t('settings.entryConfirm'), t('settings.entryConfirmTitle'), {
      confirmButtonText: t('settings.entryConfirmButton'),
      cancelButtonText: t('settings.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  entryReauth.password = ''
  entryReauth.totp = ''
  entryReauthVisible.value = true
}

async function confirmSaveEntry(): Promise<void> {
  if (!validReauth(entryReauth)) return
  const entry = entryForm.entry.trim().replace(/^\/+|\/+$/g, '')
  savingEntry.value = true
  try {
    await api.post<MessageResult>('/security/entry', {
      entry,
      password: entryReauth.password,
      totp: entryReauth.totp.trim()
    })
    entryReauthVisible.value = false
    entryReauth.password = ''
    entryReauth.totp = ''
    ElMessage.success(t('settings.entryUpdated'))
    setTimeout(() => {
      // 入口变更后重新加载，让服务端把新入口注入页面。
      window.location.replace('/dashboard')
    }, 700)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingEntry.value = false
  }
}

function openBackupDialog(): void {
  backupPassword.value = ''
  backupPasswordConfirm.value = ''
  backupReauth.password = ''
  backupReauth.totp = ''
  backupDialogVisible.value = true
}

function resetBackupDialog(): void {
  backupPassword.value = ''
  backupPasswordConfirm.value = ''
  backupReauth.password = ''
  backupReauth.totp = ''
}

async function downloadBackup(): Promise<void> {
  const password = backupPassword.value
  if (!password) {
    ElMessage.warning(t('settings.backupPasswordRequired'))
    return
  }
  if (password.length < 12) {
    ElMessage.warning(t('settings.backupPasswordMin'))
    return
  }
  if (password !== backupPasswordConfirm.value) {
    ElMessage.warning(t('settings.backupPasswordMismatch'))
    return
  }
  if (!validReauth(backupReauth)) return
  try {
    backupDownloading.value = true
    const stamp = new Date().toISOString().slice(0, 19).replace(/[-:]/g, '').replace('T', '-')
    const label = '-encrypted'
    await downloadFile('/settings/backup/download', `lactssl-backup-${stamp}${label}.zip`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ password, account_password: backupReauth.password, totp: backupReauth.totp })
    })
    backupDialogVisible.value = false
    ElMessage.success(t('settings.backupDownloaded'))
    await loadBackupInfo()
  } catch (error: any) {
    ElMessage.error(errorText(error))
  } finally {
    backupDownloading.value = false
  }
}

function openRestore(): void {
  resetRestore()
  restoreVisible.value = true
}

function resetRestore(): void {
  restoreFile.value = null
  restoreFileName.value = ''
  restoreBackupPassword.value = ''
  restoreReauth.password = ''
  restoreReauth.totp = ''
  restoreUpload.value?.clearFiles()
}

function onRestoreFileChange(file: UploadFile): void {
  const raw = file.raw
  if (!raw) return
  if (raw.size > 256 * 1024 * 1024) {
    ElMessage.warning(t('settings.restoreFileTooLarge'))
    restoreUpload.value?.clearFiles()
    restoreFile.value = null
    restoreFileName.value = ''
    return
  }
  restoreFile.value = raw
  restoreFileName.value = raw.name
}

async function restoreBackup(): Promise<void> {
  if (!restoreFile.value) {
    ElMessage.warning(t('settings.restoreFileRequired'))
    return
  }
  if (!validReauth(restoreReauth)) return
  restoring.value = true
  try {
    const form = new FormData()
    form.append('file', restoreFile.value)
    form.append('backup_password', restoreBackupPassword.value)
    form.append('account_password', restoreReauth.password)
    form.append('totp', restoreReauth.totp)
    const data = await api.post<MessageResult>('/settings/backup/restore', form, {
      'X-LACTSSL-REAUTH-PASSWORD': restoreReauth.password,
      'X-LACTSSL-REAUTH-TOTP': restoreReauth.totp.trim()
    })
    restoreVisible.value = false
    ElMessage.success(data.message || t('settings.restoreUploaded'))
    if (data.restarting) {
      // 服务会在约 1.5 秒后优雅退出，由进程管理器拉起；稍后跳转登录页。
      restoreTimer = window.setTimeout(() => {
        window.location.replace('/login')
      }, 5000)
    } else {
      await loadBackupInfo()
    }
  } catch (error: any) {
    ElMessage.error(errorText(error))
  } finally {
    restoring.value = false
  }
}

function openEmailDialog(): void {
  emailForm.email = ''
  emailForm.password = ''
  emailForm.totp = ''
  emailVisible.value = true
}

function validReauth(form: { password: string; totp: string }): boolean {
  if (!form.password) {
    ElMessage.warning(t('settings.reauthPasswordRequired'))
    return false
  }
  if (user.value?.totp_enabled && !/^\d{6}$/.test(form.totp.trim())) {
    ElMessage.warning(t('settings.reauthTotpRequired'))
    return false
  }
  return true
}

async function saveEmail(): Promise<void> {
  if (!emailForm.email) {
    ElMessage.warning(t('settings.emailRequired'))
    return
  }
  if (!validReauth(emailForm)) return
  savingAccount.value = true
  try {
    const data = await api.post<AuthResult>('/auth/change-email', {
      email: emailForm.email,
      password: emailForm.password,
      totp: emailForm.totp
    })
    state.user = data.user
    emailVisible.value = false
    ElMessage.success(t('settings.emailUpdated'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingAccount.value = false
  }
}

function openPasswordDialog(): void {
  passwordForm.new_password = ''
  passwordForm.confirm = ''
  passwordForm.password = ''
  passwordForm.totp = ''
  passwordVisible.value = true
}

async function savePassword(): Promise<void> {
  if (!validReauth(passwordForm)) return
  if ((passwordForm.new_password || '').length < 8) {
    ElMessage.warning(t('settings.passwordMin'))
    return
  }
  if (passwordForm.new_password !== passwordForm.confirm) {
    ElMessage.warning(t('settings.passwordMismatch'))
    return
  }
  savingAccount.value = true
  try {
    await api.post('/auth/change-password', {
      new_password: passwordForm.new_password,
      password: passwordForm.password,
      totp: passwordForm.totp
    })
    passwordVisible.value = false
    // 服务端已使全部会话失效并清除 Cookie，本机也回到登录页重新认证。
    logout()
    ElMessage.success(t('settings.passwordUpdated'))
    await router.replace('/login')
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingAccount.value = false
  }
}

function openPasskeyDialog(): void {
  passkeyForm.name = ''
  passkeyReauth.password = ''
  passkeyReauth.totp = ''
  passkeyVisible.value = true
}

async function addPasskey(): Promise<void> {
  if (!passkeyUsable.value) {
    ElMessage.warning(t('settings.passkeyUnavailable'))
    return
  }
  if (!validReauth(passkeyReauth)) return
  passkeyLoading.value = true
  try {
    await registerPasskey(passkeyForm.name.trim(), {
      password: passkeyReauth.password,
      totp: passkeyReauth.totp
    })
    passkeyVisible.value = false
    await Promise.all([loadPasskeys(), loadSettings(true), loadUser(true)])
    ElMessage.success(t('settings.passkeyAdded'))
  } catch (error: any) {
    ElMessage.error(isPasskeyCancelled(error) ? t('settings.passkeyCancelled') : errorText(error))
  } finally {
    passkeyLoading.value = false
  }
}

function openDeletePasskey(item: PasskeyInfo): void {
  passkeyDeleteTarget.value = item
  passkeyReauth.password = ''
  passkeyReauth.totp = ''
  passkeyDeleteVisible.value = true
}

async function deletePasskey(): Promise<void> {
  const item = passkeyDeleteTarget.value
  if (!item) return
  if (!validReauth(passkeyReauth)) return
  passkeyLoading.value = true
  try {
    await api.post(`/auth/passkeys/${item.id}/delete`, {
      password: passkeyReauth.password,
      totp: passkeyReauth.totp
    })
    passkeyDeleteVisible.value = false
    passkeyDeleteTarget.value = null
    await Promise.all([loadPasskeys(), loadSettings(true)])
    ElMessage.success(t('settings.passkeyDeleted'))
  } catch (error: any) {
    ElMessage.error(errorText(error))
  } finally {
    passkeyLoading.value = false
  }
}

async function startTOTP(): Promise<void> {
  totpVisible.value = true
  totpSetup.value = null
  totpQr.value = ''
  totpCode.value = ''
  totpReauth.password = ''
  totpReauth.totp = ''
}

async function loadTOTPSecret(): Promise<void> {
  if (!validReauth(totpReauth)) return
  totpLoading.value = true
  try {
    const data = await api.post<TOTPSetup>('/auth/totp/setup', {
      password: totpReauth.password,
      totp: totpReauth.totp
    })
    totpSetup.value = data
    totpQr.value = await QRCode.toDataURL(data.url, { width: 380, margin: 1, color: { dark: '#0f172a', light: '#ffffff' } })
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    totpLoading.value = false
  }
}

async function enableTOTP(): Promise<void> {
  if (!/^\d{6}$/.test(totpCode.value)) {
    ElMessage.warning(t('settings.totpCodeInvalid'))
    return
  }
  if (!validReauth(totpReauth)) return
  savingAccount.value = true
  try {
    await api.post('/auth/totp/enable', {
      code: totpCode.value,
      password: totpReauth.password,
      totp: totpReauth.totp
    })
    await loadUser(true)
    totpVisible.value = false
    ElMessage.success(t('settings.totpEnabled'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingAccount.value = false
  }
}

function openDisableTOTP(): void {
  totpDisableForm.password = ''
  totpDisableForm.totp = ''
  disableVisible.value = true
}

async function disableTOTP(): Promise<void> {
  if (!validReauth(totpDisableForm)) return
  savingAccount.value = true
  try {
    await api.post('/auth/totp/disable', {
      password: totpDisableForm.password,
      totp: totpDisableForm.totp
    })
    await loadUser(true)
    disableVisible.value = false
    ElMessage.success(t('settings.totpDisabled'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingAccount.value = false
  }
}

async function copySecret(): Promise<void> {
  const ok = await copyText(totpSetup.value?.secret || '')
  if (ok) ElMessage.success(t('settings.secretCopied'))
}

onMounted(() => {
  reload()
  now.value = Date.now()
  clockTimer = window.setInterval(() => {
    now.value = Date.now()
  }, 1000)
  statusTimer = window.setInterval(loadStatus, 10000)
})

onUnmounted(() => {
  if (clockTimer !== undefined) window.clearInterval(clockTimer)
  if (statusTimer !== undefined) window.clearInterval(statusTimer)
  if (restoreTimer !== undefined) window.clearTimeout(restoreTimer)
  if (configRestartTimer !== undefined) window.clearTimeout(configRestartTimer)
})
</script>

<style scoped>
/* 与 /upgrade 一致：整页固定高度，卡片头部固定，内容区滚动，主操作固定在底部。 */
.settings-card {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

/* 设置页页头与标签栏自带间距，去掉页头下外边距避免双重留白。 */
.settings-page > .page-head {
  margin-bottom: 0;
}

.settings-card > .card-head {
  flex: none;
  /* 滚动区内容（如配置文件路径信息）上滑时不能盖住卡片标题与副标题。 */
  position: relative;
  z-index: 1;
  background: var(--kp-card);
}

/* 配置文件卡片：保留上下留白，左右贴边。 */
.k-card.config-card {
  padding-top: 12px;
  padding-bottom: 20px;
  padding-left: 0;
  padding-right: 0;
}

/* 配置文件卡片：滚动区从卡片顶部开始，标签栏固定在滚动区顶部。 */
.settings-scroll {
  min-height: 0;
  /* 让内层 sticky 标签的层级限制在滚动区内部，不会盖到卡片标题。 */
  isolation: isolate;
}

.settings-footer {
  flex: none;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--kp-border);
}

.footer-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.footer-action-group {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

/* 信任链使用自适应列表而不是固定列表格，避免长主题与指纹把操作列挤出视口。 */
.trust-scroll {
  display: flex;
  flex-direction: column;
}

.trust-scroll > .el-alert,
.trust-scroll > .form-help {
  flex: none;
}

.trust-list {
  display: flex;
  flex-direction: column;
  min-height: 160px;
  border: 1px solid var(--kp-border);
  border-radius: 12px;
  overflow: hidden;
  background: #fff;
}

/* 没有数据时让空状态区域撑满卡片剩余高度。 */
.trust-list.is-empty {
  flex: 1 1 auto;
}

.trust-empty {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 44px 16px;
  color: #94a3b8;
}

.trust-empty p {
  margin: 10px 0 0;
}

.trust-item {
  flex: none;
  display: grid;
  gap: 8px;
  padding: 14px 16px;
  border-top: 1px solid #f1f5f9;
  transition: background 0.15s ease;
}

.trust-item:first-child {
  border-top: 0;
}

.trust-item:hover {
  background: #f8fbff;
}

.trust-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.trust-name {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.5;
  word-break: break-word;
}

.trust-name .el-icon {
  flex: none;
  color: #64748b;
}

.trust-head > .el-button {
  flex: none;
  margin-top: -4px;
}

.trust-subject {
  color: var(--kp-muted);
  font-size: 12px;
  line-height: 1.6;
  word-break: break-all;
}

.trust-meta {
  display: grid;
  grid-template-columns: minmax(0, 1.5fr) minmax(150px, 1fr);
  gap: 6px 24px;
}

.trust-field {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  align-items: start;
  gap: 8px;
  font-size: 12px;
  line-height: 1.6;
}

.trust-label {
  color: #94a3b8;
}

.trust-value {
  min-width: 0;
  color: #334155;
  word-break: break-all;
}

.trust-fingerprint .trust-value {
  font-size: 11px;
}

@media (max-width: 1200px) {
  .trust-meta {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 640px) {
  .trust-field {
    grid-template-columns: minmax(0, 1fr);
    gap: 2px;
  }
}

.test-hint {
  font-size: 12px;
}

.security-events {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 6px 18px;
  width: 100%;
}

.security-events-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin-bottom: 10px;
}

.security-target {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  line-height: 1.6;
}

.security-error {
  color: var(--el-color-danger);
  word-break: break-all;
  font-size: 12px;
}

.settings-section {
  margin-top: 20px;
  padding-top: 18px;
  border-top: 1px solid var(--kp-border);
}

.section-head {
  margin-bottom: 14px;
}

.section-head h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}

.section-head .sub {
  color: var(--kp-muted);
  font-size: 12px;
  margin-top: 4px;
}

.about-head {
  display: flex;
  align-items: center;
  gap: 16px;
  min-width: 0;
}

.about-build {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 4px 18px;
  margin-bottom: 18px;
}

.about-grid {
  margin-top: 0;
}

.about-panel {
  padding: 18px;
  border: 1px solid var(--kp-border);
  border-radius: 14px;
  background: #fbfcfe;
}

.about-panel .card-head {
  margin-bottom: 12px;
}

@media (min-width: 861px) {
  .page.settings-page {
    display: flex;
    flex-direction: column;
    height: calc(100vh - var(--kp-topbar-h) - 41px);
    min-height: 460px;
  }

  .settings-page .settings-tabs {
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .settings-page .settings-tabs :deep(.el-tabs__content) {
    flex: 1 1 auto;
    min-height: 0;
    overflow: hidden;
  }

  .settings-page .settings-tabs :deep(.el-tab-pane) {
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }

  .settings-card {
    height: 100%;
    margin-bottom: 0;
  }

  .settings-scroll {
    flex: 1 1 auto;
    overflow-x: hidden;
    overflow-y: auto;
    padding-right: 4px;
  }

  /* 配置表单较长，内层分组标签固定在滚动区顶部，滚动时仍可切换分组。 */
  .settings-scroll :deep(.config-tabs .el-tabs__header) {
    position: sticky;
    top: 0;
    z-index: 2;
    margin-bottom: 0;
    padding-bottom: 18px;
    background: var(--kp-card);
  }
}

@media (max-width: 860px) {
  .card-head {
    flex-wrap: wrap;
  }
}

@media (max-width: 620px) {
  .footer-actions,
  .footer-action-group {
    flex: 1 1 100%;
    width: 100%;
  }

  .footer-action-group :deep(.el-button) {
    flex: 1;
  }
}

.settings-tabs :deep(.el-tabs__header) {
  margin-bottom: 0;
}
.account-row {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  padding: 10px 2px;
  border-bottom: 1px dashed #eef1f6;
}
.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding: 10px 2px;
  border-bottom: 1px dashed #eef1f6;
}
.setting-name {
  font-weight: 600;
  font-size: 14px;
}
.entry-form {
  max-width: 520px;
}
.entry-form :deep(.el-form-item:last-of-type) {
  margin-bottom: 12px;
}
.backup-meta {
  margin-top: 8px;
}
.restore-file {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  padding: 10px 14px;
  border-radius: 10px;
  background: #f8fafc;
  font-size: 13px;
  color: #64748b;
}
.restore-file-name {
  color: #0f172a;
  word-break: break-all;
}
.setting-row:last-child {
  border-bottom: none;
  padding-bottom: 0;
}
.setting-row:first-of-type {
  margin-top: 8px;
}
.backup-card .setting-row:first-of-type {
  margin-top: 0;
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 6px;
  line-height: 1.6;
}
.passkey-list {
  display: grid;
  gap: 8px;
  padding: 6px 2px 14px;
  border-bottom: 1px dashed #eef1f6;
}

.passkey-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 12px;
  border: 1px solid #eef1f6;
  border-radius: 10px;
  background: #fbfcfe;
}

.passkey-name {
  font-weight: 600;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.totp-secret {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  background: #f8fafc;
  border-radius: 10px;
  padding: 10px 14px;
  font-size: 13px;
}
.about-feature {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 13px;
  padding: 7px 0;
  color: #334155;
}
.about-table-wrap {
  margin-top: 12px;
  overflow-x: auto;
  border: 1px solid #eef1f6;
  border-radius: 10px;
}
.about-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.about-table th,
.about-table td {
  padding: 10px 12px;
  border-bottom: 1px solid #eef1f6;
}
.about-table th {
  background: #fbfcfe;
  font-weight: 600;
  color: #475569;
  white-space: normal;
  line-height: 1.35;
}
.about-table td {
  color: #334155;
}
.about-table td.mono {
  color: #334155;
}
/* 转置布局：指标名一行、指标值一行 */
.about-table-h th,
.about-table-h td {
  text-align: center;
  vertical-align: middle;
  border-right: 1px solid #eef1f6;
}
.about-table-h td {
  white-space: nowrap;
}
.about-table-h th:last-child,
.about-table-h td:last-child {
  border-right: none;
}
.about-table-h tbody tr td {
  padding-top: 12px;
  padding-bottom: 12px;
}
.about-table-h tbody tr:last-child td {
  border-bottom: none;
}

.proxy-info {
  padding: 4px 0 12px;
}
.proxy-suggestions {
  display: grid;
  gap: 10px;
}
.proxy-suggestion {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid #eef1f6;
  border-radius: 12px;
  background: #fbfcfe;
}
.restart-info {
  margin-bottom: 14px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #f8fafc;
}
@media (max-width: 640px) {
  .proxy-suggestion {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
