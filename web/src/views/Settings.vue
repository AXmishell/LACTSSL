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
              <el-button v-else type="danger" plain @click="disableVisible = true">{{ t('settings.disable') }}</el-button>
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
              <div style="font-weight: 600; font-size: 14px">{{ t('settings.criticalVerifyTitle') }}</div>
              <div class="muted" style="margin-top: 4px">{{ t('settings.criticalVerifySub') }}</div>
            </div>
            <div class="flex gap-10 wrap">
              <span
                class="tag-soft"
                :class="verifyMode === 'none' ? 'tag-red' : verifyMode === 'password' ? 'tag-gray' : 'tag-green'"
              >
                {{ verifyModeLabel }}
              </span>
              <el-button @click="openVerifyModeDialog">{{ t('settings.switchVerifyMode') }}</el-button>
            </div>
          </div>

          <div class="setting-row">
            <div>
              <div style="font-weight: 600; font-size: 14px">{{ t('settings.mailRecovery') }}</div>
              <div class="muted" style="margin-top: 4px">{{ t('settings.mailRecoverySub') }}</div>
            </div>
            <el-button @click="goNotifications">{{ t('settings.goConfigure') }}</el-button>
          </div>
        </div>

        <!-- 安全入口 -->
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
            <el-alert
              v-if="sessionStrongAuth"
              type="success"
              :closable="false"
              show-icon
              :title="t('settings.strongAuthHint')"
              style="margin-bottom: 16px"
            />
            <el-form-item v-else-if="verifyModeNeedsPassword(verifyMode)" :label="t('settings.currentPassword')" required>
              <el-input v-model="entryForm.password" type="password" show-password :placeholder="t('settings.confirmIdentity')" />
            </el-form-item>
            <el-form-item v-if="!sessionStrongAuth && verifyModeNeedsTOTP(verifyMode)" :label="t('settings.totpCode')" required>
              <el-input
                v-model="entryForm.totp"
                maxlength="6"
                :placeholder="t('settings.totpCodePlaceholder')"
                @keyup.enter="saveEntry"
              />
              <div class="form-help">{{ t('settings.criticalVerifyTotpHint') }}</div>
            </el-form-item>
            <el-form-item v-if="!sessionStrongAuth && verifyModeNeedsPasskey(verifyMode)" :label="t('settings.passkeyVerify')" required>
              <div class="flex gap-10 wrap" style="align-items: center">
                <el-button
                  :loading="entryPasskeyLoading"
                  :disabled="!passkeyUsable"
                  @click="acquireEntryPasskeyProof"
                >
                  {{ entryPasskeyProof ? t('settings.passkeyVerified') : t('settings.passkeyVerifyAction') }}
                </el-button>
                <span v-if="entryPasskeyProof" class="tag-soft tag-green">{{ t('settings.passkeyVerified') }}</span>
              </div>
              <div class="form-help">{{ t('settings.passkeyVerifyHint') }}</div>
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
                  <th scope="col">{{ t('settings.vmSize') }}</th>
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
                  <td class="mono">{{ formatBytes(status?.memory_vsz) }}</td>
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
        <el-form-item :label="t('settings.currentPassword')">
          <el-input v-model="emailForm.password" type="password" show-password />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="emailVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="savingAccount" @click="saveEmail">{{ t('settings.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 修改密码 -->
    <el-dialog v-model="passwordVisible" :title="t('settings.changePasswordTitle')" width="440px">
      <el-form label-position="top">
        <el-form-item :label="t('settings.oldPassword')">
          <el-input v-model="passwordForm.old_password" type="password" show-password />
        </el-form-item>
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
        <div style="text-align: center; padding: 8px 0 16px">
          <img v-if="totpQr" :src="totpQr" alt="TOTP QR" style="width: 190px; height: 190px; border-radius: 12px; border: 1px solid #eef1f6" />
        </div>
        <div class="totp-secret">
          <span class="mono">{{ totpSetup?.secret }}</span>
          <el-button text type="primary" size="small" @click="copySecret">{{ t('settings.copy') }}</el-button>
        </div>
        <el-form label-position="top" style="margin-top: 18px">
          <el-form-item :label="t('settings.currentPassword')">
            <el-input v-model="totpPassword" type="password" show-password autocomplete="current-password" />
          </el-form-item>
          <el-form-item :label="t('settings.totpCodeLabel')">
            <el-input v-model="totpCode" maxlength="6" :placeholder="t('settings.totpCodePlaceholder')" @keyup.enter="enableTOTP" />
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="totpVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="primary" :loading="savingAccount" :disabled="!totpSetup" @click="enableTOTP">{{ t('settings.confirmEnable') }}</el-button>
      </template>
    </el-dialog>

    <!-- 关闭 TOTP -->
    <el-dialog v-model="disableVisible" :title="t('settings.disableTotpTitle')" width="420px">
      <el-form label-position="top">
        <el-form-item :label="t('settings.currentPassword')">
          <el-input v-model="disableForm.password" type="password" show-password />
        </el-form-item>
        <el-form-item :label="t('settings.totpCode')">
          <el-input v-model="disableForm.code" maxlength="6" :placeholder="t('settings.totpCodePlaceholder')" />
        </el-form-item>
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
        <el-form-item :label="t('settings.currentPassword')">
          <el-input v-model="passkeyForm.password" type="password" show-password autocomplete="current-password" />
          <div class="form-help">{{ t('settings.passkeyPasswordHint') }}</div>
        </el-form-item>
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
        <el-form-item :label="t('settings.currentPassword')">
          <el-input v-model="passkeyDeletePassword" type="password" show-password autocomplete="current-password" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="passkeyDeleteVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button type="danger" :loading="passkeyLoading" @click="deletePasskey">{{ t('common.delete') }}</el-button>
      </template>
    </el-dialog>

    <!-- 二次验证方式 -->
    <el-dialog v-model="verifyModeVisible" :title="t('settings.criticalVerify')" width="680px" @closed="resetVerifyModeDialog">
      <p class="muted" style="margin-top: 0">{{ t('settings.criticalVerifySub') }}</p>
      <el-form label-position="top">
        <el-form-item :label="t('settings.criticalVerifyMethod')">
          <el-radio-group v-model="verifyModeForm.mode" style="display: flex; flex-wrap: wrap; gap: 8px 16px">
            <el-radio value="none">{{ t('settings.verifyModeNone') }}</el-radio>
            <el-radio value="password">{{ t('settings.verifyModePassword') }}</el-radio>
            <el-radio value="totp" :disabled="!user?.totp_enabled">{{ t('settings.verifyModeTOTP') }}</el-radio>
            <el-radio value="password_totp" :disabled="!user?.totp_enabled">
              {{ t('settings.verifyModePasswordTOTP') }}
            </el-radio>
            <el-radio value="passkey" :disabled="!passkeys.length">{{ t('settings.verifyModePasskey') }}</el-radio>
          </el-radio-group>
          <div v-if="!user?.totp_enabled" class="form-help">{{ t('settings.verifyModeTotpDisabled') }}</div>
          <div v-if="!passkeys.length" class="form-help">{{ t('settings.verifyModePasskeyDisabled') }}</div>
        </el-form-item>
        <template v-if="!sessionStrongAuth">
          <el-form-item v-if="verifyModeNeedsPassword(verifyMode)" :label="t('settings.currentPassword')" required>
            <el-input
              v-model="verifyModeForm.password"
              type="password"
              show-password
              autocomplete="current-password"
            />
          </el-form-item>
          <el-form-item v-if="verifyModeNeedsTOTP(verifyMode)" :label="t('settings.totpCode')" required>
            <el-input v-model="verifyModeForm.totp" maxlength="6" :placeholder="t('settings.totpCodePlaceholder')" />
          </el-form-item>
          <el-form-item v-if="verifyModeNeedsPasskey(verifyMode)" :label="t('settings.passkeyVerify')" required>
            <div class="flex gap-10 wrap" style="align-items: center">
              <el-button
                :loading="verifyModePasskeyLoading"
                :disabled="!passkeyUsable"
                @click="acquireVerifyModePasskeyProof"
              >
                {{ verifyModePasskeyProof ? t('settings.passkeyVerified') : t('settings.passkeyVerifyAction') }}
              </el-button>
              <span v-if="verifyModePasskeyProof" class="tag-soft tag-green">{{ t('settings.passkeyVerified') }}</span>
            </div>
          </el-form-item>
        </template>
        <el-alert
          v-else
          type="success"
          :closable="false"
          show-icon
          :title="t('settings.strongAuthHint')"
          style="margin-bottom: 12px"
        />
        <div v-if="verifyMode === 'none'" class="form-help">
          {{ t('settings.criticalVerifyNoneHint') }}
        </div>
      </el-form>
      <template v-if="verifyModeForm.mode !== 'none'">
        <el-divider content-position="left">{{ t('settings.stepUpTitle') }}</el-divider>
        <p class="muted" style="margin: 0 0 10px">{{ t('settings.stepUpSub') }}</p>
        <div class="step-up-list">
          <div v-for="item in stepUpToggleDefs" :key="item.action" class="step-up-row">
            <span>{{ t(item.labelKey) }}</span>
            <el-switch v-model="stepUpForm[item.action]" />
          </div>
        </div>
      </template>
      <el-alert
        v-if="verifyModeForm.mode === 'none' && verifyMode !== 'none'"
        type="warning"
        :closable="false"
        show-icon
        :title="t('settings.criticalVerifyNoneWarning')"
        style="margin-bottom: 12px"
      />
      <el-alert
        type="info"
        :closable="false"
        show-icon
        :title="t('settings.criticalVerifySwitchHint')"
      />
      <template #footer>
        <el-button @click="verifyModeVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button
          type="primary"
          :loading="savingVerifyMode"
          :disabled="!verifyDialogDirty"
          @click="saveVerifyMode"
        >
          {{ t('common.confirm') }}
        </el-button>
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
        :title="t('settings.backupReauthHint')"
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
        <el-alert
          v-if="sessionStrongAuth"
          type="success"
          :closable="false"
          show-icon
          :title="t('settings.strongAuthHint')"
          style="margin-bottom: 16px"
        />
        <el-form-item
          v-if="!sessionStrongAuth && verifyModeNeedsPassword(verifyMode)"
          :label="t('settings.restorePassword')"
          required
        >
          <el-input
            v-model="restorePassword"
            type="password"
            show-password
            :placeholder="t('settings.restorePasswordPlaceholder')"
          />
        </el-form-item>
        <el-form-item
          v-if="!sessionStrongAuth && verifyModeNeedsTOTP(verifyMode)"
          :label="t('settings.totpCode')"
          required
        >
          <el-input
            v-model="restoreTotp"
            maxlength="6"
            :placeholder="t('settings.totpCodePlaceholder')"
          />
          <div class="form-help">{{ t('settings.criticalVerifyTotpHint') }}</div>
        </el-form-item>
        <el-form-item
          v-if="!sessionStrongAuth && verifyModeNeedsPasskey(verifyMode)"
          :label="t('settings.passkeyVerify')"
          required
        >
          <div class="flex gap-10 wrap" style="align-items: center">
            <el-button
              :loading="restorePasskeyLoading"
              :disabled="!passkeyUsable"
              @click="acquireRestorePasskeyProof"
            >
              {{ restorePasskeyProof ? t('settings.passkeyVerified') : t('settings.passkeyVerifyAction') }}
            </el-button>
            <span v-if="restorePasskeyProof" class="tag-soft tag-green">{{ t('settings.passkeyVerified') }}</span>
          </div>
          <div class="form-help">{{ t('settings.passkeyVerifyHint') }}</div>
        </el-form-item>
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
      </el-form>
      <template #footer>
        <el-button @click="restoreVisible = false">{{ t('settings.cancel') }}</el-button>
        <el-button
          type="danger"
          :loading="restoring"
          :disabled="!restoreFile || !restoreVerifyReady"
          @click="restoreBackup"
        >
          {{ t('settings.confirmRestore') }}
        </el-button>
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
import { Document, Refresh, UploadFilled } from '@element-plus/icons-vue'
import QRCode from 'qrcode'
import { api, copyText, downloadFile, errorText } from '../api'
import {
  requestSecondaryVerification,
  secondaryVerifyHeader,
  verifyModeNeedsPassword,
  verifyModeNeedsPasskey,
  verifyModeNeedsTOTP,
  verifySecondary
} from '../secondaryVerify'
import { isPasskeyCancelled, passkeySecureContext, passkeySupported, registerPasskey, requestPasskeyProof } from '../passkeys'
import { loadMeta, loadSettings, loadUser, logout, state } from '../store'
import { formatDate } from '../utils'
import type { AuthResult, BackupInfo, MessageResult, Meta, PasskeyInfo, SecondaryVerifyAction, Settings as AppSettings, TOTPSetup, VerifyMode } from '../types'

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
const sessionStrongAuth = computed(() => !!state.user?.strong_auth)
const passkeys = ref<PasskeyInfo[]>([])
const passkeyLoading = ref(false)
const passkeyUsable = computed(() => passkeySupported() && passkeySecureContext())
const verifyMode = computed<VerifyMode>(() => {
  const mode = settings.value?.verify_mode
  if (mode === 'none') return 'none'
  if ((mode === 'totp' || mode === 'password_totp') && user.value?.totp_enabled) {
    return mode
  }
  if (
    mode === 'passkey' &&
    (settings.value?.passkey_enabled || passkeys.value.length > 0)
  ) {
    return mode
  }
  return 'password'
})

const verifyModeLabel = computed(() => {
  switch (verifyMode.value) {
    case 'none':
      return t('settings.verifyModeNone')
    case 'totp':
      return t('settings.verifyModeTOTP')
    case 'password_totp':
      return t('settings.verifyModePasswordTOTP')
    case 'passkey':
      return t('settings.verifyModePasskey')
    default:
      return t('settings.verifyModePassword')
  }
})

const savingEntry = ref(false)
const savingAccount = ref(false)
const savingVerifyMode = ref(false)

const entryForm = reactive({ entry: '', password: '', totp: '' })

const verifyModeVisible = ref(false)
const verifyModeForm = reactive({
  mode: 'password' as VerifyMode,
  password: '',
  totp: ''
})
const stepUpForm = reactive<Record<string, boolean>>({})
const stepUpToggleDefs: Array<{ action: SecondaryVerifyAction; labelKey: string }> = [
  { action: 'deploy_execute', labelKey: 'secondaryVerify.action.deployExecute' },
  { action: 'task_write', labelKey: 'secondaryVerify.action.taskWrite' },
  { action: 'account_write', labelKey: 'secondaryVerify.action.accountWrite' },
  { action: 'settings_write', labelKey: 'secondaryVerify.action.settingsWrite' },
  { action: 'notification_test', labelKey: 'secondaryVerify.action.notificationTest' },
  { action: 'acme_write', labelKey: 'secondaryVerify.action.acmeWrite' },
  { action: 'private_ca_write', labelKey: 'secondaryVerify.action.privateCaWrite' },
  { action: 'certificate_key_export', labelKey: 'secondaryVerify.action.certificateKeyExport' },
  { action: 'certificate_write', labelKey: 'secondaryVerify.action.certificateWrite' },
  { action: 'monitor_write', labelKey: 'secondaryVerify.action.monitorWrite' }
]
const stepUpDirty = computed(() =>
  stepUpToggleDefs.some(
    (item) => stepUpForm[item.action] !== (settings.value?.step_up?.[item.action] !== false)
  )
)
const verifyDialogDirty = computed(() => verifyModeForm.mode !== verifyMode.value || stepUpDirty.value)

const emailVisible = ref(false)
const emailForm = reactive({ email: '', password: '' })
const passwordVisible = ref(false)
const passwordForm = reactive({ old_password: '', new_password: '', confirm: '' })

const totpVisible = ref(false)
const totpLoading = ref(false)
const totpSetup = ref<TOTPSetup | null>(null)
const totpQr = ref('')
const totpCode = ref('')
const totpPassword = ref('')
const disableVisible = ref(false)
const disableForm = reactive({ password: '', code: '' })

const passkeyVisible = ref(false)
const passkeyForm = reactive({ name: '', password: '' })
const passkeyDeleteVisible = ref(false)
const passkeyDeleteTarget = ref<PasskeyInfo | null>(null)
const passkeyDeletePassword = ref('')
const verifyModePasskeyProof = ref('')
const verifyModePasskeyLoading = ref(false)
const entryPasskeyProof = ref('')
const entryPasskeyLoading = ref(false)
const restorePasskeyProof = ref('')
const restorePasskeyLoading = ref(false)

const backup = ref<BackupInfo | null>(null)
const backupDownloading = ref(false)
const backupDialogVisible = ref(false)
const backupPassword = ref('')
const backupPasswordConfirm = ref('')
const restoreVisible = ref(false)
const restoreUpload = ref<UploadInstance>()
const restoreFile = ref<File | null>(null)
const restoreFileName = ref('')
const restorePassword = ref('')
const restoreTotp = ref('')
const restoreBackupPassword = ref('')
const restoring = ref(false)
const restoreVerifyReady = computed(() => {
  if (sessionStrongAuth.value || verifyMode.value === 'none') return true
  if (verifyModeNeedsPassword(verifyMode.value) && !restorePassword.value) {
    return false
  }
  if (verifyModeNeedsTOTP(verifyMode.value) && !/^\d{6}$/.test(restoreTotp.value)) {
    return false
  }
  if (verifyModeNeedsPasskey(verifyMode.value) && !restorePasskeyProof.value) {
    return false
  }
  return true
})
let restoreTimer: number | undefined

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

async function reload(): Promise<void> {
  try {
    const [metaData, settingData] = await Promise.all([loadMeta(true), loadSettings(true)])
    meta.value = metaData
    applySettings(settingData)
    await Promise.all([loadStatus(), loadUser(true), loadBackupInfo(), loadPasskeys()])
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

// verifyCredentialsValid 按当前验证方式校验表单中填写的关键操作凭证。
// 返回 false 时已经提示用户具体缺少哪一项。
function verifyCredentialsValid(
  mode: VerifyMode,
  password: string,
  code: string,
  passkeyProof = ''
): boolean {
  if (sessionStrongAuth.value || mode === 'none') return true
  if (verifyModeNeedsPassword(mode) && !password) {
    ElMessage.warning(t('settings.currentPasswordRequired'))
    return false
  }
  if (verifyModeNeedsTOTP(mode) && !/^\d{6}$/.test(code)) {
    ElMessage.warning(t('settings.totpCodeInvalid'))
    return false
  }
  if (verifyModeNeedsPasskey(mode) && !passkeyProof) {
    ElMessage.warning(t('settings.passkeyRequired'))
    return false
  }
  return true
}

// acquirePasskeyProof 供内联验证表单获取短期通行密钥证明。
async function acquirePasskeyProof(target: { value: string }, loading: { value: boolean }): Promise<void> {
  if (!passkeyUsable.value) {
    ElMessage.warning(t('settings.passkeyUnavailable'))
    return
  }
  loading.value = true
  try {
    target.value = await requestPasskeyProof()
    ElMessage.success(t('settings.passkeyVerified'))
  } catch (error: any) {
    ElMessage.error(isPasskeyCancelled(error) ? t('settings.passkeyCancelled') : errorText(error))
  } finally {
    loading.value = false
  }
}

function acquireVerifyModePasskeyProof(): Promise<void> {
  return acquirePasskeyProof(verifyModePasskeyProof, verifyModePasskeyLoading)
}

function acquireEntryPasskeyProof(): Promise<void> {
  return acquirePasskeyProof(entryPasskeyProof, entryPasskeyLoading)
}

function acquireRestorePasskeyProof(): Promise<void> {
  return acquirePasskeyProof(restorePasskeyProof, restorePasskeyLoading)
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
  if (!verifyCredentialsValid(verifyMode.value, entryForm.password, entryForm.totp, entryPasskeyProof.value)) {
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
    const token = await verifySecondary('entry_change', entryForm.password, entryForm.totp, entryPasskeyProof.value)
    await api.post<MessageResult>('/security/entry', { entry }, { [secondaryVerifyHeader]: token })
    ElMessage.success(t('settings.entryUpdated'))
    entryForm.password = ''
    entryForm.totp = ''
    entryPasskeyProof.value = ''
    setTimeout(() => {
      // 入口变更后重新加载，让服务端把新入口注入页面。
      window.location.replace('/dashboard')
    }, 700)
  } catch (error: any) {
    if (error?.totpRequired || error?.passkeyRequired) {
      await refreshVerifyMode()
    }
    ElMessage.error(error.message)
    savingEntry.value = false
  }
}

function resetStepUpForm(): void {
  for (const item of stepUpToggleDefs) {
    stepUpForm[item.action] = settings.value?.step_up?.[item.action] !== false
  }
}

function openVerifyModeDialog(): void {
  verifyModeForm.mode = verifyMode.value
  verifyModeForm.password = ''
  verifyModeForm.totp = ''
  verifyModePasskeyProof.value = ''
  resetStepUpForm()
  verifyModeVisible.value = true
}

function resetVerifyModeDialog(): void {
  verifyModeForm.password = ''
  verifyModeForm.totp = ''
  verifyModePasskeyProof.value = ''
}

// 其他会话说切换验证方式后，本地设置可能过期；收到 totp_required 时重新拉取，
// 让入口和恢复备份表单立即切换为验证码输入框。
async function refreshVerifyMode(): Promise<void> {
  try {
    settings.value = await loadSettings(true)
  } catch (_) {
    // 刷新失败时保留原界面，用户仍可手动刷新页面。
  }
}

async function saveVerifyMode(): Promise<void> {
  if (!verifyDialogDirty.value) {
    return
  }
  if (
    verifyModeForm.mode === 'totp' ||
    verifyModeForm.mode === 'password_totp'
  ) {
    if (!user.value?.totp_enabled) {
      ElMessage.warning(t('settings.verifyModeTotpDisabled'))
      return
    }
  }
  if (verifyModeForm.mode === 'passkey') {
    if (passkeys.value.length === 0) {
      ElMessage.warning(t('settings.verifyModePasskeyDisabled'))
      return
    }
  }
  // 切换验证方式本身必须用“当前生效的验证方式”确认身份。
  if (!verifyCredentialsValid(verifyMode.value, verifyModeForm.password, verifyModeForm.totp, verifyModePasskeyProof.value)) {
    return
  }
  savingVerifyMode.value = true
  try {
    await api.post('/security/verify-mode', {
      mode: verifyModeForm.mode,
      password: verifyModeForm.password,
      totp: verifyModeForm.totp,
      passkey_proof: verifyModePasskeyProof.value,
      step_up: { ...stepUpForm }
    })
    settings.value = await loadSettings(true)
    resetStepUpForm()
    verifyModeVisible.value = false
    ElMessage.success(t('settings.verifyModeUpdated'))
  } catch (error: any) {
    if (error?.passkeyRequired || error?.totpRequired) {
      await refreshVerifyMode()
    }
    ElMessage.error(error.message)
  } finally {
    savingVerifyMode.value = false
  }
}

function openBackupDialog(): void {
  backupPassword.value = ''
  backupPasswordConfirm.value = ''
  backupDialogVisible.value = true
}

function resetBackupDialog(): void {
  backupPassword.value = ''
  backupPasswordConfirm.value = ''
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
  try {
    // 点击下载后先完成独立的二次验证，再携带短期 token 下载备份。
    const token = await requestSecondaryVerification('backup_download')
    if (token === null) return
    backupDownloading.value = true
    const stamp = new Date().toISOString().slice(0, 19).replace(/[-:]/g, '').replace('T', '-')
    const label = '-encrypted'
    await downloadFile('/settings/backup/download', `lactssl-backup-${stamp}${label}.zip`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        [secondaryVerifyHeader]: token
      },
      body: JSON.stringify({ password })
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
  restorePassword.value = ''
  restoreTotp.value = ''
  restorePasskeyProof.value = ''
  restoreBackupPassword.value = ''
  restoreUpload.value?.clearFiles()
}

function onRestoreFileChange(file: UploadFile): void {
  const raw = file.raw
  if (!raw) return
  if (raw.size > 256 * 1024 * 1024) {
    ElMessage.warning(t('settings.restoreFileTooLarge'))
    restoreUpload.value?.clearFiles()
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
  if (!verifyCredentialsValid(verifyMode.value, restorePassword.value, restoreTotp.value, restorePasskeyProof.value)) {
    return
  }
  restoring.value = true
  try {
    const token = await verifySecondary('backup_restore', restorePassword.value, restoreTotp.value, restorePasskeyProof.value)
    const form = new FormData()
    form.append('file', restoreFile.value)
    form.append('backup_password', restoreBackupPassword.value)
    const data = await api.post<MessageResult>('/settings/backup/restore', form, {
      [secondaryVerifyHeader]: token
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
    if (error?.totpRequired) {
      await refreshVerifyMode()
    }
    ElMessage.error(errorText(error))
  } finally {
    restoring.value = false
  }
}

function openEmailDialog(): void {
  emailForm.email = ''
  emailForm.password = ''
  emailVisible.value = true
}

async function saveEmail(): Promise<void> {
  if (!emailForm.email || !emailForm.password) {
    ElMessage.warning(t('settings.emailPasswordRequired'))
    return
  }
  savingAccount.value = true
  try {
    const data = await api.post<AuthResult>('/auth/change-email', { email: emailForm.email, password: emailForm.password })
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
  passwordForm.old_password = ''
  passwordForm.new_password = ''
  passwordForm.confirm = ''
  passwordVisible.value = true
}

async function savePassword(): Promise<void> {
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
      old_password: passwordForm.old_password,
      new_password: passwordForm.new_password
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
  passkeyForm.password = ''
  passkeyVisible.value = true
}

async function addPasskey(): Promise<void> {
  if (!passkeyForm.password) {
    ElMessage.warning(t('settings.currentPasswordRequired'))
    return
  }
  if (!passkeyUsable.value) {
    ElMessage.warning(t('settings.passkeyUnavailable'))
    return
  }
  passkeyLoading.value = true
  try {
    await registerPasskey(passkeyForm.name.trim(), passkeyForm.password)
    passkeyVisible.value = false
    passkeyForm.password = ''
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
  passkeyDeletePassword.value = ''
  passkeyDeleteVisible.value = true
}

async function deletePasskey(): Promise<void> {
  const item = passkeyDeleteTarget.value
  if (!item) return
  if (!passkeyDeletePassword.value) {
    ElMessage.warning(t('settings.currentPasswordRequired'))
    return
  }
  passkeyLoading.value = true
  try {
    await api.post(`/auth/passkeys/${item.id}/delete`, {
      password: passkeyDeletePassword.value
    })
    passkeyDeleteVisible.value = false
    passkeyDeleteTarget.value = null
    passkeyDeletePassword.value = ''
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
  totpLoading.value = true
  totpCode.value = ''
  totpPassword.value = ''
  totpSetup.value = null
  totpQr.value = ''
  try {
    const data = await api.post<TOTPSetup>('/auth/totp/setup')
    totpSetup.value = data
    totpQr.value = await QRCode.toDataURL(data.url, { width: 380, margin: 1, color: { dark: '#0f172a', light: '#ffffff' } })
  } catch (error: any) {
    ElMessage.error(error.message)
    totpVisible.value = false
  } finally {
    totpLoading.value = false
  }
}

async function enableTOTP(): Promise<void> {
  if (!totpPassword.value) {
    ElMessage.warning(t('settings.currentPasswordRequired'))
    return
  }
  if (!/^\d{6}$/.test(totpCode.value)) {
    ElMessage.warning(t('settings.totpCodeInvalid'))
    return
  }
  savingAccount.value = true
  try {
    await api.post('/auth/totp/enable', { code: totpCode.value, password: totpPassword.value })
    await loadUser(true)
    totpVisible.value = false
    totpPassword.value = ''
    ElMessage.success(t('settings.totpEnabled'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    savingAccount.value = false
  }
}

async function disableTOTP(): Promise<void> {
  savingAccount.value = true
  try {
    await api.post('/auth/totp/disable', { password: disableForm.password, code: disableForm.code })
    await loadUser(true)
    disableVisible.value = false
    disableForm.password = ''
    disableForm.code = ''
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
.step-up-list {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 14px;
  max-height: 360px;
  overflow-y: auto;
  padding: 10px 12px;
  border: 1px solid #eef1f6;
  border-radius: 10px;
  background: #f8fafc;
}
.step-up-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-width: 0;
  padding: 6px 10px;
  border: 1px solid #eef1f6;
  border-radius: 8px;
  background: #fff;
  font-size: 13px;
  line-height: 1.4;
  color: #334155;
}
.step-up-row > span {
  flex: 1;
  min-width: 0;
}
.step-up-row :deep(.el-switch) {
  flex-shrink: 0;
}
@media (max-width: 640px) {
  .step-up-list {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
