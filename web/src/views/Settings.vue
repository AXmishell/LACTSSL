<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('settings.title') }}</h1>
        <p>{{ t('settings.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle @click="reload" />
      </div>
    </div>

    <el-tabs v-model="tab" class="settings-tabs">
      <!-- 账户安全 -->
      <el-tab-pane :label="t('settings.tabSecurity')" name="security">
        <div class="k-card">
          <div class="card-head">
            <div>
              <h3>{{ t('settings.loginAccount') }}</h3>
              <div class="sub">{{ t('settings.loginAccountSub') }}</div>
            </div>
          </div>
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
        </div>

        <!-- 门禁入口 -->
        <div class="k-card">
          <div class="card-head">
            <div>
              <h3>{{ t('settings.entryTitle') }}</h3>
              <div class="sub">{{ t('settings.entrySub') }}</div>
            </div>
          </div>
          <el-form :model="entryForm" label-position="top" class="entry-form">
            <el-form-item>
              <el-input v-model="entryForm.entry" :placeholder="settings?.entry || t('settings.entryPlaceholder')" />
              <div class="form-help">{{ t('settings.entryHelp') }}</div>
            </el-form-item>
            <el-button type="primary" :loading="savingEntry" @click="saveEntry">{{ t('settings.updateEntry') }}</el-button>
          </el-form>
        </div>
      </el-tab-pane>

      <!-- 备份与恢复 -->
      <el-tab-pane :label="t('settings.tabBackup')" name="backup">
        <div class="k-card">
          <div class="card-head">
            <div>
              <h3>{{ t('settings.backupTitle') }}</h3>
              <div class="sub">{{ t('settings.backupSub') }}</div>
            </div>
          </div>

          <div class="setting-row">
            <div>
              <div class="setting-name">{{ t('settings.backupDownloadTitle') }}</div>
              <div class="muted" style="margin-top: 4px">{{ t('settings.backupDownloadSub') }}</div>
            </div>
            <el-button type="primary" :loading="backupDownloading" @click="openBackupDialog">
              {{ t('settings.backupDownload') }}
            </el-button>
          </div>

          <div class="setting-row">
            <div>
              <div class="setting-name">{{ t('settings.backupRestoreTitle') }}</div>
              <div class="muted" style="margin-top: 4px">{{ t('settings.backupRestoreSub') }}</div>
            </div>
            <el-button type="danger" plain @click="openRestore">{{ t('settings.backupRestore') }}</el-button>
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
      </el-tab-pane>

      <!-- TLS 信任链 -->
      <el-tab-pane :label="t('settings.tabTrust')" name="trust">
        <div class="k-card">
          <div class="card-head">
            <div>
              <h3>{{ t('settings.trustTitle') }}</h3>
              <div class="sub">{{ t('settings.trustSub') }}</div>
            </div>
            <div class="flex gap-10 wrap">
              <el-button :icon="Download" @click="downloadTrustBundle">{{ t('settings.trustDownload') }}</el-button>
              <el-button type="primary" :icon="Upload" @click="openTrustDialog">{{ t('settings.trustImport') }}</el-button>
            </div>
          </div>

          <el-alert
            type="warning"
            :closable="false"
            show-icon
            :title="t('settings.trustAlert')"
            style="margin-bottom: 16px"
          />

          <el-table :data="trustedCAs" v-loading="trustLoading" size="small" style="width: 100%">
            <el-table-column min-width="220" :label="t('settings.trustCA')">
              <template #default="{ row }">
                <div style="font-weight: 600">{{ row.name }}</div>
                <div class="muted mono" style="font-size: 12px; margin-top: 2px; word-break: break-all">{{ row.subject }}</div>
              </template>
            </el-table-column>
            <el-table-column min-width="180" :label="t('settings.trustIssuer')">
              <template #default="{ row }">
                <span class="mono" style="font-size: 12px; word-break: break-all">{{ row.issuer }}</span>
              </template>
            </el-table-column>
            <el-table-column width="170" :label="t('settings.trustExpires')">
              <template #default="{ row }">
                <span>{{ formatDate(row.not_after) }}</span>
              </template>
            </el-table-column>
            <el-table-column min-width="190" :label="t('settings.trustFingerprint')">
              <template #default="{ row }">
                <span class="mono" style="font-size: 11px; word-break: break-all">{{ row.fingerprint }}</span>
              </template>
            </el-table-column>
            <el-table-column width="80" align="right" :label="t('common.actions')">
              <template #default="{ row }">
                <el-button text type="danger" size="small" @click="removeTrustedCA(row)">{{ t('common.delete') }}</el-button>
              </template>
            </el-table-column>
            <template #empty>
              <div style="padding: 34px 0; color: #94a3b8">
                <el-icon size="28"><Lock /></el-icon>
                <p style="margin: 10px 0 0">{{ t('settings.trustEmpty') }}</p>
              </div>
            </template>
          </el-table>
          <div class="form-help" style="margin-top: 12px">{{ t('settings.trustBuiltinNote') }}</div>
        </div>
      </el-tab-pane>

      <!-- 关于 -->
      <el-tab-pane :label="t('settings.tabAbout')" name="about">
        <div class="k-card about-card">
          <img src="/logo.svg" alt="LACTSSL" style="width: 58px; height: 58px" />
          <div>
            <h2 style="margin: 0 0 6px">LACTSSL</h2>
            <div class="muted">{{ t('settings.aboutDesc') }}</div>
            <div class="muted" style="margin-top: 8px; display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 4px 18px">
              <span>{{ t('settings.buildVersion') }}</span>
              <span class="mono">{{ meta?.version || '1.0.0' }}</span>
              <span>{{ t('settings.buildTime') }}</span>
              <span class="mono">{{ formatDate(status?.build_time) }}</span>
              <span>{{ t('settings.buildArch') }}</span>
              <span class="mono">{{ status?.build_arch || t('common.none') }}</span>
            </div>
          </div>
        </div>
        <div class="grid grid-2" style="margin-top: 18px">
          <div class="k-card">
            <div class="card-head"><h3>{{ t('settings.features') }}</h3></div>
            <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature1') }}</div>
            <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature2') }}</div>
            <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature3') }}</div>
            <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature4') }}</div>
            <div class="about-feature"><el-icon color="#10b981"><CircleCheck /></el-icon>{{ t('settings.feature5') }}</div>
          </div>
          <div class="k-card">
            <div class="card-head"><h3>{{ t('settings.techStack') }}</h3></div>
            <div class="about-line"><span>{{ t('settings.backend') }}</span><span class="mono">Go + Gin + SQLite</span></div>
            <div class="about-line"><span>{{ t('settings.frontend') }}</span><span class="mono">Vue 3 + Vite + Element Plus</span></div>
            <div class="about-line"><span>{{ t('settings.acme') }}</span><span class="mono">lego / RFC 8555</span></div>
            <div class="about-line"><span>{{ t('settings.storage') }}</span><span class="mono">SQLite (encrypted)</span></div>
            <div class="about-line"><span>{{ t('settings.repo') }}</span><a href="https://github.com/AXmishell/LACTSSL" target="_blank" rel="noreferrer">github.com/AXmishell/LACTSSL</a></div>
          </div>
        </div>
        <div class="k-card" style="margin-top: 18px">
          <div class="card-head"><h3>{{ t('settings.systemInfo') }}</h3></div>
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
      </el-tab-pane>
    </el-tabs>

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
import { api, copyText, downloadFile, errorText } from '../api'
import { isPasskeyCancelled, passkeySecureContext, passkeySupported, registerPasskey } from '../passkeys'
import { loadMeta, loadSettings, loadUser, logout, state } from '../store'
import { formatDate } from '../utils'
import type { AuthResult, BackupInfo, MessageResult, Meta, PasskeyInfo, Settings as AppSettings, TOTPSetup, TrustedCA } from '../types'

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

const entryForm = reactive({ entry: '' })

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

function applySettings(data: AppSettings): void {
  settings.value = data
  // 默认填入当前入口，便于直接编辑。
  entryForm.entry = data.entry || ''
}

function goNotifications(): void {
  router.push('/notifications')
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
    const [metaData, settingData] = await Promise.all([loadMeta(true), loadSettings(true)])
    meta.value = metaData
    applySettings(settingData)
    await Promise.all([loadStatus(), loadUser(true), loadBackupInfo(), loadPasskeys(), loadTrustedCAs()])
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
  savingEntry.value = true
  try {
    await api.post<MessageResult>('/security/entry', { entry })
    ElMessage.success(t('settings.entryUpdated'))
    setTimeout(() => {
      // 入口变更后重新加载，让服务端把新入口注入页面。
      window.location.replace('/dashboard')
    }, 700)
  } catch (error: any) {
    ElMessage.error(error.message)
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
    const data = await api.post<MessageResult>('/settings/backup/restore', form)
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
})
</script>

<style scoped>
.settings-tabs :deep(.el-tabs__header) {
  margin-bottom: 20px;
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
.about-card {
  display: flex;
  align-items: center;
  gap: 18px;
  background: linear-gradient(135deg, #ffffff, #f5f9ff);
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
</style>
