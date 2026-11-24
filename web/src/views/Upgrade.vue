<template>
  <div class="page upgrade-page">
    <div class="page-head">
      <div>
        <h1>{{ t('upgrade.title') }}</h1>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle :loading="loading" @click="load" />
      </div>
    </div>

    <el-tabs v-model="tab" v-loading="loading" class="upgrade-tabs">
      <el-tab-pane :label="t('upgrade.tabs.program')" name="program">
        <div class="k-card upgrade-card">
          <div class="card-head">
            <div>
              <h3>{{ t('upgrade.program.title') }}</h3>
              <div v-if="program" class="sub">
                {{ t('upgrade.program.subtitle', { repo: program.repo, channel: program.channel }) }}
              </div>
              <div v-else class="sub">{{ t('upgrade.program.unavailable') }}</div>
            </div>
            <el-tag v-if="program" effect="plain" round size="small">{{ program.platform }}</el-tag>
          </div>

          <template v-if="program">
            <div class="program-grid upgrade-summary">
              <div>
                <div class="stat-label">{{ t('upgrade.program.current') }}</div>
                <div class="program-value mono">{{ program.current_version }}</div>
              </div>
              <div>
                <div class="stat-label">{{ t('upgrade.program.latest') }}</div>
                <div class="program-value mono">{{ program.latest ? `v${program.latest.version}` : t('common.none') }}</div>
              </div>
              <div>
                <div class="stat-label">{{ t('upgrade.program.published') }}</div>
                <div class="program-value">
                  {{ program.latest?.published_at ? formatDate(program.latest.published_at) : t('common.none') }}
                </div>
              </div>
              <div>
                <div class="stat-label">{{ t('upgrade.program.checked') }}</div>
                <div class="program-value">{{ program.checked_at ? formatDate(program.checked_at) : t('common.none') }}</div>
              </div>
            </div>

            <div class="upgrade-scroll program-detail">
              <el-alert
                v-if="program.restart_pending"
                type="success"
                :closable="false"
                show-icon
                :title="t('upgrade.program.restartPending', { version: program.last_run?.to_version || program.latest?.version })"
                :description="program.restart_mode === 'exit' ? t('upgrade.program.restartExitHint') : t('upgrade.program.restartHint')"
              />

              <el-collapse v-if="program.latest?.notes">
                <el-collapse-item :title="t('upgrade.program.releaseNotes')" name="notes">
                  <pre class="release-notes">{{ program.latest.notes }}</pre>
                </el-collapse-item>
              </el-collapse>

              <el-alert
                v-if="program.last_run?.error"
                type="error"
                :closable="false"
                show-icon
                :title="program.last_run.error"
                style="margin-top: 12px"
              />
              <div v-if="program.last_run" class="muted program-last-run">
                {{ t('upgrade.program.lastRun', {
                  from: program.last_run.from_version,
                  to: program.last_run.to_version,
                  time: formatDate(program.last_run.finished_at)
                }) }}
                <span v-if="program.last_run.backup_path" class="mono break-all"> · {{ program.last_run.backup_path }}</span>
              </div>
            </div>

            <div class="upgrade-footer">
              <div class="upgrade-status">
                <el-tooltip :content="programStatusLine.text" placement="top" :show-after="300">
                  <el-alert
                    :type="programStatusLine.type"
                    :title="programStatusLine.text"
                    :closable="false"
                    show-icon
                    class="status-line-alert"
                  />
                </el-tooltip>
              </div>
              <div class="footer-actions is-auto">
                <div class="footer-action-group">
                  <el-button
                    :icon="RefreshRight"
                    :loading="programChecking"
                    :disabled="!program.enabled"
                    @click="checkProgram"
                  >
                    {{ t('upgrade.program.check') }}
                  </el-button>
                </div>
                <div v-if="program.restart_pending || program.can_update" class="footer-action-group">
                  <el-button
                    v-if="program.restart_pending"
                    type="success"
                    :icon="SwitchButton"
                    :loading="programRestarting"
                    @click="restartProgram"
                  >
                    {{ t('upgrade.program.restartNow') }}
                  </el-button>
                  <el-button
                    v-else
                    type="primary"
                    :icon="Download"
                    :loading="programApplying"
                    @click="applyProgram"
                  >
                    {{ t('upgrade.program.updateTo', { version: program.latest?.version }) }}
                  </el-button>
                </div>
              </div>
            </div>
          </template>

          <div v-else class="upgrade-scroll">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              :title="t('upgrade.program.unavailable')"
            />
          </div>
        </div>
      </el-tab-pane>

      <el-tab-pane :label="t('upgrade.tabs.database')" name="database">
        <div class="k-card upgrade-card">
          <div class="card-head">
            <div>
              <h3>{{ t('upgrade.database.title') }}</h3>
              <div class="sub">
                {{ t('upgrade.database.current', {
                  driver: dbStatus?.source.driver || 'sqlite',
                  target: dbStatus?.source.path || dbStatus?.source.dsn || t('common.none')
                }) }}
              </div>
            </div>
            <el-tag v-if="dbStatus?.running" type="warning" effect="plain" round size="small">
              {{ t('upgrade.database.running') }}
            </el-tag>
          </div>

          <div class="database-grid">
            <div>
              <div class="stat-label">{{ t('upgrade.database.targetDriver') }}</div>
              <el-select v-model="dbTargetDriver" style="width: 100%" :disabled="dbStatus?.running">
                <el-option value="postgres" label="PostgreSQL 18" />
                <el-option value="mysql" label="MySQL 5.7+" />
                <el-option value="sqlite" label="SQLite" />
              </el-select>
            </div>
            <div v-if="dbTargetDriver !== 'sqlite'">
              <div class="stat-label">{{ t('upgrade.database.dsn') }}</div>
              <el-radio-group v-model="dbDSNMode" size="small" :disabled="dbStatus?.running">
                <el-radio-button value="form">{{ t('upgrade.database.modeForm') }}</el-radio-button>
                <el-radio-button value="raw">{{ t('upgrade.database.modeRaw') }}</el-radio-button>
              </el-radio-group>
            </div>
            <div v-else>
              <div class="stat-label">{{ t('upgrade.database.path') }}</div>
              <el-input v-model="dbTargetPath" class="mono" placeholder="data/lactssl-converted.db" :disabled="dbStatus?.running" />
            </div>
            <div class="database-force">
              <el-checkbox v-model="dbForce" :disabled="dbStatus?.running || sameDriver">
                {{ t('upgrade.database.force') }}
              </el-checkbox>
              <div v-if="sameDriver" class="form-help">{{ t('upgrade.database.sameDriverForceHint') }}</div>
            </div>
          </div>

          <div class="upgrade-scroll">
            <div v-if="dbTargetDriver !== 'sqlite'" class="dsn-block">
            <el-form
              v-if="dbDSNMode === 'form'"
              ref="dsnFormRef"
              :model="dbDSNForm"
              :rules="dsnRules"
              label-position="left"
              label-width="96px"
              require-asterisk-position="right"
              :disabled="dbStatus?.running"
              class="dsn-form"
            >
              <div class="dsn-grid">
                <el-form-item :label="t('upgrade.database.host')" prop="host">
                  <el-input v-model="dbDSNForm.host" placeholder="db.example.com" autocomplete="off" />
                </el-form-item>
                <el-form-item :label="t('upgrade.database.port')" prop="port">
                  <el-input-number v-model="dbDSNForm.port" :min="1" :max="65535" controls-position="right" align="left" style="width: 100%" />
                </el-form-item>
                <el-form-item :label="t('upgrade.database.username')" prop="username">
                  <el-input v-model="dbDSNForm.username" :placeholder="dbTargetDriver === 'mysql' ? 'root' : 'postgres'" autocomplete="off" />
                </el-form-item>
                <el-form-item :label="t('upgrade.database.dbPassword')">
                  <el-input v-model="dbDSNForm.password" type="password" show-password autocomplete="new-password" />
                </el-form-item>
                <el-form-item :label="t('upgrade.database.dbName')" prop="database">
                  <el-input v-model="dbDSNForm.database" placeholder="lactssl" autocomplete="off" />
                </el-form-item>
                <el-form-item :label="t('upgrade.database.sslMode')" prop="sslmode">
                  <el-select v-model="dbDSNForm.sslmode" style="width: 100%">
                    <el-option v-for="mode in tlsModes" :key="mode" :value="mode" :label="mode" />
                  </el-select>
                </el-form-item>
              </div>
              <div class="form-help">{{ t('upgrade.database.dsnHelp') }}</div>
              <div class="dsn-preview">
                <span class="stat-label">{{ t('upgrade.database.preview') }}</span>
                <code class="mono break-all">{{ (dsnRevealed ? targetDSN : maskedTargetDSN) || t('common.none') }}</code>
                <el-button text type="primary" size="small" @click="dsnRevealed = !dsnRevealed">
                  {{ t(dsnRevealed ? 'upgrade.database.hide' : 'upgrade.database.reveal') }}
                </el-button>
                <el-button text type="primary" size="small" :icon="CopyDocument" @click="copyTargetDSN">
                  {{ t('common.copy') }}
                </el-button>
              </div>
            </el-form>

            <template v-else>
              <el-input
                v-model="dbRawDSN"
                class="mono"
                type="textarea"
                :rows="2"
                :disabled="dbStatus?.running"
                :placeholder="rawDSNPlaceholder"
              />
              <div class="flex gap-10 wrap" style="margin-top: 10px">
                <el-button size="small" :disabled="dbStatus?.running || !dbRawDSN.trim()" @click="parseRawDSN">
                  {{ t('upgrade.database.parseDsn') }}
                </el-button>
                <span class="muted">{{ t('upgrade.database.rawDsnHelp') }}</span>
              </div>
            </template>
            </div>
          </div>

          <div class="upgrade-footer">
            <div class="upgrade-status">
            <div class="flex gap-10 wrap" style="margin-bottom: 8px">
              <el-button :loading="dbTesting" :disabled="dbStatus?.running" @click="testDatabaseTarget">
                {{ t('upgrade.database.test') }}
              </el-button>
              <el-button :loading="dbPreflighting" :disabled="dbStatus?.running" @click="preflightDatabaseConvert">
                {{ t('upgrade.database.preflight') }}
              </el-button>
            </div>

            <el-tooltip :content="dbStatusLine.text" placement="top" :show-after="300">
              <el-alert
                :type="dbStatusLine.type"
                :title="dbStatusLine.text"
                :closable="false"
                show-icon
                class="status-line-alert"
              />
            </el-tooltip>
            </div>

            <div class="upgrade-progress">
              <div class="progress-meta">
                <span class="stat-label">{{ t('upgrade.database.progress') }}</span>
                <span class="muted">
                  {{ t('upgrade.database.phaseLabel', { phase: dbProgressLabel }) }}
                  <span v-if="dbStatus?.progress.table"> · {{ dbStatus.progress.table }}</span>
                  <span v-if="dbStatus?.progress.table_total">
                    ({{ dbStatus.progress.table_index }}/{{ dbStatus.progress.table_total }})
                  </span>
                </span>
                <span class="muted">
                  {{ t('upgrade.database.rows', {
                    rows: dbStatus?.progress.rows || 0,
                    total: dbStatus?.progress.total_rows || 0
                  }) }}
                </span>
                <span class="muted progress-percent">{{ dbPercent }}%</span>
              </div>
              <el-progress
                :percentage="dbPercent"
                :status="dbStatus?.state?.phase === 'failed' ? 'exception' : undefined"
                :show-text="false"
                :stroke-width="6"
                style="margin-top: 6px"
              />
            </div>

            <div class="footer-actions">
              <div class="footer-auth-pair">
                <div class="footer-action-group footer-auth-group">
                  <el-input v-model="dbPassword" class="auth-password" type="password" show-password :placeholder="t('upgrade.database.password')" />
                </div>
                <div v-if="totpEnabled" class="footer-action-group footer-auth-group">
                  <el-input v-model="dbTOTP" class="auth-totp" :placeholder="t('upgrade.database.totp')" />
                </div>
              </div>

              <div class="footer-action-pair">
                <div class="footer-action-group">
                  <span class="math-check" :class="{ 'is-error': dbMathError }">
                    <el-input
                      v-model="dbMathAnswer"
                      :placeholder="t('upgrade.database.mathPlaceholder')"
                      style="width: 160px"
                      @input="dbMathError = false"
                      @keyup.enter="startDatabaseConvert"
                    >
                      <template #prefix>
                        <span class="math-question">{{ t('upgrade.database.mathPrompt', { a: dbMathA, b: dbMathB }) }}</span>
                      </template>
                    </el-input>
                  </span>
                  <el-button type="primary" :loading="dbStarting" :disabled="dbStatus?.running" @click="startDatabaseConvert">
                    {{ t('upgrade.database.start') }}
                  </el-button>
                  <el-button v-if="dbStatus?.running" @click="cancelDatabaseConvert">
                    {{ t('common.cancel') }}
                  </el-button>
                </div>

                <div v-if="rollbackVisible" class="footer-action-group">
                  <span class="math-check" :class="{ 'is-error': dbRollbackMathError }">
                    <el-input
                      v-model="dbRollbackMathAnswer"
                      :placeholder="t('upgrade.database.mathPlaceholder')"
                      style="width: 160px"
                      @input="dbRollbackMathError = false"
                      @keyup.enter="rollbackDatabaseConvert"
                    >
                      <template #prefix>
                        <span class="math-question">{{ t('upgrade.database.mathPrompt', { a: dbRollbackMathA, b: dbRollbackMathB }) }}</span>
                      </template>
                    </el-input>
                  </span>
                  <el-button type="danger" plain :loading="dbRollingBack" @click="rollbackDatabaseConvert">
                    {{ t('upgrade.database.rollback') }}
                  </el-button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <el-tab-pane :label="t('upgrade.tabs.maintenance')" name="maintenance">
        <div v-if="status" class="k-card upgrade-card">
          <div class="card-head">
            <div>
              <h3>{{ t('upgrade.actionsTitle') }}</h3>
              <div class="sub">{{ t('upgrade.actionsSub') }}</div>
            </div>
            <div class="form-help upgrade-head-state">
              {{ t('upgrade.autoState', { state: status.auto_enabled ? t('common.enabled') : t('common.disabled') }) }}
              · {{ t('upgrade.vacuumState', { state: status.vacuum_allowed ? t('common.enabled') : t('common.disabled') }) }}
            </div>
          </div>

          <div class="upgrade-summary maintenance-summary">
            <div>
              <div class="stat-label">{{ t('upgrade.appVersion') }}</div>
              <div class="stat-value">{{ status.app_version || t('common.none') }}</div>
              <div class="muted">{{ t('upgrade.checkedAt', { time: formatDate(status.checked_at) }) }}</div>
            </div>
            <div>
              <div class="stat-label">{{ t('upgrade.dataVersion') }}</div>
              <div class="stat-value">v{{ status.stored_version }} → v{{ status.current_version }}</div>
              <div class="muted">{{ status.version_label }}</div>
            </div>
            <div>
              <div class="stat-label">{{ t('upgrade.databaseSize') }}</div>
              <div class="stat-value">{{ formatBytes(status.database.file_size + status.database.wal_size) }}</div>
              <div class="muted">{{ t('upgrade.freePages', { size: formatBytes(status.database.free_bytes) }) }}</div>
            </div>
            <div>
              <div class="stat-label">{{ t('upgrade.integrity') }}</div>
              <div class="stat-value" :class="status.database.integrity === 'ok' ? 'text-green' : 'text-red'">
                {{ status.database.integrity }}
              </div>
              <div class="muted">
                {{ t('upgrade.dbMeta', { tables: status.database.tables, indexes: status.database.indexes, mode: status.database.journal_mode }) }}
              </div>
            </div>
          </div>

          <div class="upgrade-scroll">
            <div v-if="report" class="result-block">
              <div class="result-head">
                <h3>{{ t('upgrade.lastResult') }}</h3>
                <div class="sub">
                  {{ t('upgrade.resultSummary', {
                    from: report.from_version,
                    to: report.to_version,
                    changes: report.config.changes.length,
                    reclaimed: formatBytes(report.database.reclaimed_bytes)
                  }) }}
                </div>
              </div>
              <div v-if="report.backup_dir" class="muted mono break-all">{{ t('upgrade.backupDir') }}: {{ report.backup_dir }}</div>
              <el-alert
                v-for="warning in report.warnings"
                :key="warning"
                type="warning"
                :closable="false"
                show-icon
                :title="warning"
                style="margin-top: 8px"
              />
              <ul v-if="report.database.actions.length" class="action-list">
                <li v-for="action in report.database.actions" :key="action">{{ action }}</li>
              </ul>
            </div>
          </div>

          <div v-if="status.warnings.length" class="upgrade-warnings">
            <el-alert
              v-for="warning in status.warnings"
              :key="warning"
              type="warning"
              :closable="false"
              show-icon
              :title="warning"
            />
          </div>

          <div class="upgrade-footer">
            <div class="upgrade-status">
              <el-tooltip :content="maintenanceStatusLine.text" placement="top" :show-after="300">
                <el-alert
                  :type="maintenanceStatusLine.type"
                  :title="maintenanceStatusLine.text"
                  :closable="false"
                  show-icon
                  class="status-line-alert"
                />
              </el-tooltip>
            </div>
            <div class="footer-actions is-auto">
              <div class="footer-action-group">
                <el-switch v-model="vacuum" :disabled="!status.vacuum_allowed" :active-text="t('upgrade.vacuum')" />
              </div>
              <div class="footer-action-group">
                <el-button :icon="Refresh" :loading="optimizing" @click="optimize">{{ t('upgrade.optimizeOnly') }}</el-button>
              </div>
              <div class="footer-action-group">
                <el-button type="primary" :icon="MagicStick" :loading="running" @click="runAll">{{ t('upgrade.runAll') }}</el-button>
              </div>
            </div>
          </div>
        </div>
        <el-alert
          v-else
          type="info"
          :closable="false"
          show-icon
          :title="t('common.loading')"
        />
      </el-tab-pane>

      <el-tab-pane :label="t('upgrade.tabs.config')" name="config">
        <div v-if="status" class="k-card upgrade-card upgrade-table-card">
          <div class="card-head">
            <div>
              <h3>{{ t('upgrade.configTitle') }}</h3>
              <div class="sub">{{ t('upgrade.configSub', { path: status.config.path || t('common.none'), count: status.config.changes.length }) }}</div>
            </div>
          </div>
          <div class="upgrade-scroll">
            <div class="table-scroll">
              <el-table :data="status.config.changes" height="100%" style="width: 100%">
                <el-table-column min-width="220" prop="key" :label="t('upgrade.configKey')" />
                <el-table-column width="110" :label="t('upgrade.actionLabel')">
                  <template #default="{ row }">
                    <el-tag :type="row.action === 'added' ? 'success' : 'info'" effect="light" round size="small">
                      {{ t(`upgrade.action.${row.action}`) }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column min-width="160" :label="t('upgrade.before')">
                  <template #default="{ row }"><span class="mono break-all">{{ row.before || t('common.none') }}</span></template>
                </el-table-column>
                <el-table-column min-width="160" :label="t('upgrade.after')">
                  <template #default="{ row }"><span class="mono break-all">{{ row.after || t('common.none') }}</span></template>
                </el-table-column>
                <template #empty>
                  <div style="padding: 36px 0; color: #94a3b8">{{ t('upgrade.noConfigChanges') }}</div>
                </template>
              </el-table>
            </div>
          </div>
        </div>
        <el-alert
          v-else
          type="info"
          :closable="false"
          show-icon
          :title="t('common.loading')"
        />
      </el-tab-pane>

      <el-tab-pane :label="t('upgrade.tabs.history')" name="history">
        <div v-if="status?.last_run" class="k-card upgrade-card">
          <div class="card-head">
            <div>
              <h3>{{ t('upgrade.lastRunTitle') }}</h3>
              <div class="sub">
                {{ t('upgrade.lastRunDesc', {
                  source: status.last_run.source,
                  time: formatDate(status.last_run.finished_at),
                  steps: status.last_run.changed_steps,
                  changes: status.last_run.config_changes,
                  reclaimed: formatBytes(status.last_run.reclaimed_bytes)
                }) }}
              </div>
            </div>
          </div>
          <div class="upgrade-scroll">
            <el-alert v-if="status.last_run.error" type="error" :closable="false" show-icon :title="status.last_run.error" />
            <el-alert
              v-for="warning in status.last_run.warnings"
              :key="warning"
              type="warning"
              :closable="false"
              show-icon
              :title="warning"
              style="margin-top: 8px"
            />
          </div>
        </div>
        <el-alert
          v-else
          type="info"
          :closable="false"
          show-icon
          :title="t('upgrade.noHistory')"
        />
      </el-tab-pane>
    </el-tabs>
    <el-dialog v-model="programReauthVisible" :title="t('upgrade.program.updateTo', { version: programPendingVersion })" width="420px" destroy-on-close>
      <ReauthFields
        v-model:password="programReauth.password"
        v-model:totp="programReauth.totp"
        :totp-enabled="!!state.user?.totp_enabled"
      />
      <template #footer>
        <el-button @click="programReauthVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="programApplying" @click="confirmApplyProgram">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { CopyDocument, Download, MagicStick, Refresh, RefreshRight, SwitchButton } from '@element-plus/icons-vue'
import { api, apiBase, copyText, errorText } from '../api'
import { t } from '../i18n'
import ReauthFields from '../components/ReauthFields.vue'
import { loadUser, state } from '../store'
import { formatDate } from '../utils'
import type {
  DatabaseConvertStatus,
  DatabasePreflightReport,
  ProgramRunReport,
  ProgramStatus,
  UpgradeRunReport,
  UpgradeStatus
} from '../types'

const status = ref<UpgradeStatus | null>(null)
const report = ref<UpgradeRunReport | null>(null)
const loading = ref(false)
const tab = ref('program')
let initialTabPicked = false
const running = ref(false)
const optimizing = ref(false)
const vacuum = ref(true)
const program = ref<ProgramStatus | null>(null)
const programChecking = ref(false)
const programApplying = ref(false)
const programReauthVisible = ref(false)
const programPendingVersion = ref('')
const programReauth = reactive({ password: '', totp: '' })
const programRestarting = ref(false)
const dbStatus = ref<DatabaseConvertStatus | null>(null)
const dbTargetDriver = ref('postgres')
const dbTargetPath = ref('')
const dbForce = ref(false)
const dbDSNMode = ref<'form' | 'raw'>('form')
const dbRawDSN = ref('')
const dbDSNForm = reactive({
  host: '',
  port: 5432 as number | null,
  username: '',
  password: '',
  database: '',
  sslmode: 'disable'
})
const dsnFormRef = ref<FormInstance>()
const dsnRevealed = ref(false)
const sslModes = ['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full']
// go-sql-driver/mysql 使用 tls 参数，取值集合与 PostgreSQL 的 sslmode 不同。
const mysqlTLSModes = ['disable', 'preferred', 'require', 'skip-verify']
const tlsModes = computed(() => (dbTargetDriver.value === 'mysql' ? mysqlTLSModes : sslModes))
const rawDSNPlaceholder = computed(() =>
  dbTargetDriver.value === 'mysql'
    ? 'mysql://user:pass@host:3306/db?tls=preferred'
    : 'postgres://user:pass@host:5432/db?sslmode=disable'
)
const dbTesting = ref(false)
const dbPreflighting = ref(false)
const dbStarting = ref(false)
const dbPreflight = ref<DatabasePreflightReport | null>(null)
const dbTargetMessage = ref('')
const dbTargetError = ref(false)
const dbPassword = ref('')
const dbTOTP = ref('')
const dbMathA = ref(0)
const dbMathB = ref(0)
const dbMathAnswer = ref('')
const dbMathError = ref(false)
const dbRollbackMathA = ref(0)
const dbRollbackMathB = ref(0)
const dbRollbackMathAnswer = ref('')
const dbRollbackMathError = ref(false)
const dbRollingBack = ref(false)
let dbPollTimer: number | undefined

newMathChallenge()
newRollbackMathChallenge()

const totpEnabled = computed(() => !!state.user?.totp_enabled)
const sameDriver = computed(() => !!dbStatus.value && dbTargetDriver.value === dbStatus.value.source.driver)
// 切换完成后，底部操作组由“开始切换”切换为“一键回滚”，避免同时出现两套算术和按钮。
const rollbackVisible = computed(() => {
  const state = dbStatus.value?.state
  return state?.phase === 'completed' && state.from_driver !== state.to_driver
})
// 程序升级与数据库切换共用同一条底部状态栏：只显示当前最关键的一条状态。
const programStatusLine = computed<{ text: string; type: 'error' | 'warning' | 'success' | 'info' }>(() => {
  const p = program.value
  if (!p) return { text: t('upgrade.program.unavailable'), type: 'info' }
  if (p.check_error) {
    return { text: t('upgrade.program.checkFailed', { error: p.check_error }), type: 'error' }
  }
  if (p.restart_pending) {
    return {
      text: t('upgrade.program.restartPending', {
        version: p.last_run?.to_version || p.latest?.version || ''
      }),
      type: 'success'
    }
  }
  if (p.last_run?.error) return { text: p.last_run.error, type: 'error' }
  if (p.can_update) {
    return { text: t('upgrade.program.found', { version: p.latest?.version || '' }), type: 'info' }
  }
  if (p.reason) return { text: p.reason, type: p.update_available ? 'warning' : 'info' }
  return { text: t('upgrade.program.latestMessage'), type: 'success' }
})
// 升级与优化标签页把原顶部的迁移提示收敛到底部状态栏，避免内容区上下各占一块。
const maintenanceStatusLine = computed<{ text: string; type: 'warning' | 'success' | 'info' }>(() => {
  const s = status.value
  if (!s) return { text: t('common.loading'), type: 'info' }
  if (s.needs_upgrade) {
    return {
      text: `${t('upgrade.pendingTitle')} · ${t('upgrade.pendingDesc', {
        stored: s.stored_version,
        current: s.current_version
      })}`,
      type: 'warning'
    }
  }
  return {
    text: `${t('upgrade.upToDateTitle')} · ${t('upgrade.upToDateDesc', { version: s.current_version })}`,
    type: 'success'
  }
})
// 底部状态栏一次只显示一条提示，长文本用 tooltip 查看完整内容。
const dbStatusLine = computed<{ text: string; type: 'error' | 'warning' | 'success' | 'info' }>(() => {
  if (dbTargetMessage.value) {
    return { text: dbTargetMessage.value, type: dbTargetError.value ? 'error' : 'success' }
  }
  if (dbStatus.value?.state?.error) {
    return { text: dbStatus.value.state.error, type: 'error' }
  }
  if (sameDriver.value && dbStatus.value?.state?.phase === 'completed') {
    return { text: t('upgrade.database.sameDriverNoRollback'), type: 'warning' }
  }
  if (sameDriver.value) {
    return { text: t('upgrade.database.sameDriverAlert'), type: 'warning' }
  }
  if (dbPreflight.value) {
    return {
      text: t('upgrade.database.preflightSummary', {
        tables: dbPreflight.value.source_tables,
        rows: dbPreflight.value.source_rows,
        size: formatBytes(dbPreflight.value.source_bytes),
        empty: dbPreflight.value.target_empty ? t('common.yes') : t('common.no')
      }),
      type: 'info'
    }
  }
  if (dbStatus.value?.report?.backup_path) {
    return { text: `${t('upgrade.database.backup')}: ${dbStatus.value.report.backup_path}`, type: 'info' }
  }
  return { text: t('upgrade.database.warning'), type: 'warning' }
})
// 同构迁移不允许清空目标库。
watch(sameDriver, (value) => {
  if (value) dbForce.value = false
})
// 切换驱动时同步默认端口和 TLS 选项，避免把 PostgreSQL 的 sslmode 带到 MySQL。
watch(dbTargetDriver, (driver) => {
  if (driver === 'mysql') {
    if (!dbDSNForm.port || dbDSNForm.port === 5432) dbDSNForm.port = 3306
    if (!mysqlTLSModes.includes(dbDSNForm.sslmode)) dbDSNForm.sslmode = 'preferred'
  } else if (driver === 'postgres') {
    if (!dbDSNForm.port || dbDSNForm.port === 3306) dbDSNForm.port = 5432
    if (!sslModes.includes(dbDSNForm.sslmode)) dbDSNForm.sslmode = 'disable'
  }
})
const dbProgressLabel = computed(() => t(`upgrade.database.phase.${dbStatus.value?.state?.phase || dbStatus.value?.progress.phase || 'idle'}`))
const dbPercent = computed(() => {
  const progress = dbStatus.value?.progress
  if (!progress) return 0
  if (progress.total_rows > 0) return Math.min(100, Math.round((progress.rows / progress.total_rows) * 100))
  if (progress.table_total > 0) return Math.min(100, Math.round((progress.table_index / progress.table_total) * 100))
  return 0
})

// 表单只是输入层：提交前统一拼成标准 URL DSN，后端、CLI 与配置文件格式保持不变。
const targetDSN = computed(() => {
  if (dbTargetDriver.value === 'sqlite') return ''
  if (dbDSNMode.value === 'raw') return dbRawDSN.value.trim()
  const host = dbDSNForm.host.trim()
  const username = dbDSNForm.username.trim()
  const database = dbDSNForm.database.trim()
  if (!host || !username || !database) return ''
  const mysql = dbTargetDriver.value === 'mysql'
  const port = Number(dbDSNForm.port) || (mysql ? 3306 : 5432)
  const hostPart = host.includes(':') && !host.startsWith('[') ? `[${host}]` : host
  const auth = `${encodeURIComponent(username)}${dbDSNForm.password ? `:${encodeURIComponent(dbDSNForm.password)}` : ''}@`
  const params = new URLSearchParams(
    mysql ? { tls: dbDSNForm.sslmode || 'preferred' } : { sslmode: dbDSNForm.sslmode || 'disable' }
  )
  const scheme = mysql ? 'mysql' : 'postgres'
  return `${scheme}://${auth}${hostPart}:${port}/${encodeURIComponent(database)}?${params.toString()}`
})

const maskedTargetDSN = computed(() => {
  if (!targetDSN.value) return ''
  try {
    const parsed = new URL(targetDSN.value)
    if (parsed.password) parsed.password = '***'
    return parsed.toString()
  } catch (_) {
    return targetDSN.value
  }
})

const dsnRules = computed<FormRules>(() => ({
  host: [
    {
      validator: (_rule, value, callback) => {
        const host = String(value || '').trim()
        if (!host) {
          callback(new Error(t('upgrade.database.hostRequired')))
          return
        }
        if (host.includes('://') || /[\s/?#@]/.test(host)) {
          callback(new Error(t('upgrade.database.hostInvalid')))
          return
        }
        callback()
      },
      trigger: 'blur'
    }
  ],
  port: [
    {
      validator: (_rule, value, callback) => {
        const port = Number(value)
        if (!value || !Number.isInteger(port) || port < 1 || port > 65535) {
          callback(new Error(t('upgrade.database.portInvalid')))
          return
        }
        callback()
      },
      trigger: 'change'
    }
  ],
  username: [{ required: true, message: t('upgrade.database.usernameRequired'), trigger: 'blur' }],
  database: [{ required: true, message: t('upgrade.database.dbNameRequired'), trigger: 'blur' }]
}))

// 简单算术验证只是前端的误操作防呆；真正的敏感操作仍由密码/TOTP 与后端校验保护。
function newMathChallenge(): void {
  dbMathA.value = 1 + Math.floor(Math.random() * 9)
  dbMathB.value = 1 + Math.floor(Math.random() * 9)
  dbMathAnswer.value = ''
  dbMathError.value = false
}

function verifyMathChallenge(): boolean {
  const raw = dbMathAnswer.value.trim()
  const answer = Number(raw)
  if (!raw || !Number.isInteger(answer) || answer !== dbMathA.value + dbMathB.value) {
    newMathChallenge()
    dbMathError.value = true
    ElMessage.error(t('upgrade.database.mathWrong'))
    return false
  }
  dbMathError.value = false
  return true
}

function newRollbackMathChallenge(): void {
  dbRollbackMathA.value = 1 + Math.floor(Math.random() * 9)
  dbRollbackMathB.value = 1 + Math.floor(Math.random() * 9)
  dbRollbackMathAnswer.value = ''
  dbRollbackMathError.value = false
}

function verifyRollbackMathChallenge(): boolean {
  const raw = dbRollbackMathAnswer.value.trim()
  const answer = Number(raw)
  if (!raw || !Number.isInteger(answer) || answer !== dbRollbackMathA.value + dbRollbackMathB.value) {
    newRollbackMathChallenge()
    dbRollbackMathError.value = true
    ElMessage.error(t('upgrade.database.mathWrong'))
    return false
  }
  dbRollbackMathError.value = false
  return true
}

// 表单模式下交给 Element Plus 校验；原生模式只要求非空，其余交由后端解析。
async function validateDatabaseTarget(): Promise<boolean> {
  if (dbTargetDriver.value === 'sqlite' && !dbTargetPath.value.trim()) {
    dbTargetError.value = true
    dbTargetMessage.value = t('upgrade.database.pathRequired')
    ElMessage.warning(t('upgrade.database.pathRequired'))
    return false
  }
  if (dbTargetDriver.value === 'sqlite') return true
  if (dbDSNMode.value === 'raw') {
    if (!dbRawDSN.value.trim()) {
      dbTargetError.value = true
      dbTargetMessage.value = t('upgrade.database.rawDsnRequired')
      return false
    }
    return true
  }
  if (!dsnFormRef.value) return false
  try {
    await dsnFormRef.value.validate()
    return true
  } catch (_) {
    return false
  }
}

function parseRawDSN(): void {
  const raw = dbRawDSN.value.trim()
  if (!raw) return
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch (_) {
    ElMessage.error(t('upgrade.database.parseFailed'))
    return
  }
  // Unix socket、多主机等无法用固定字段表达的 DSN 保留在原生模式。
  const protocol = parsed.protocol.toLowerCase()
  const mysql = protocol === 'mysql:' || protocol === 'mariadb:'
  if ((!mysql && protocol !== 'postgres:' && protocol !== 'postgresql:') || !parsed.hostname) {
    ElMessage.error(t('upgrade.database.parseFailed'))
    return
  }
  dbTargetDriver.value = mysql ? 'mysql' : 'postgres'
  dbDSNForm.host = parsed.hostname.replace(/^\[(.*)\]$/, '$1')
  dbDSNForm.port = parsed.port ? Number(parsed.port) : mysql ? 3306 : 5432
  dbDSNForm.username = decodeURIComponent(parsed.username)
  dbDSNForm.password = decodeURIComponent(parsed.password)
  dbDSNForm.database = decodeURIComponent(parsed.pathname.replace(/^\//, ''))
  const mode = mysql
    ? parsed.searchParams.get('tls') || parsed.searchParams.get('sslmode') || ''
    : parsed.searchParams.get('sslmode') || ''
  const modes = mysql ? mysqlTLSModes : sslModes
  if (modes.includes(mode)) dbDSNForm.sslmode = mode
  dbDSNMode.value = 'form'
  ElMessage.success(t('upgrade.database.parseOk'))
}

async function copyTargetDSN(): Promise<void> {
  if (!targetDSN.value) return
  const copied = await copyText(targetDSN.value)
  if (copied) ElMessage.success(t('common.copied'))
  else ElMessage.error(t('common.copyFailed'))
}

async function loadDatabaseStatus(): Promise<void> {
  try {
    dbStatus.value = await api.get<DatabaseConvertStatus>('/upgrade/database')
    if (dbStatus.value.source.driver !== 'sqlite') {
      dbTargetDriver.value = 'sqlite'
    }
    if (dbStatus.value.running) {
      startDatabasePolling()
    }
  } catch (_) {
    dbStatus.value = null
  }
}

function startDatabasePolling(): void {
  if (dbPollTimer) return
  dbPollTimer = window.setInterval(async () => {
    try {
      const next = await api.get<DatabaseConvertStatus>('/upgrade/database')
      dbStatus.value = next
      if (next.state?.phase === 'switching' || next.state?.phase === 'completed') {
        stopDatabasePolling()
        await waitForRestart()
        return
      }
      if (!next.running && next.state?.phase === 'failed') {
        stopDatabasePolling()
      }
    } catch (_) {
      // 切换重启期间请求会失败，继续等待。
    }
  }, 1000)
}

function stopDatabasePolling(): void {
  if (dbPollTimer) {
    window.clearInterval(dbPollTimer)
    dbPollTimer = undefined
  }
}

function databaseTargetBody(): Record<string, unknown> {
  return {
    target_driver: dbTargetDriver.value,
    target_path: dbTargetPath.value,
    target_dsn: targetDSN.value,
    force: sameDriver.value ? false : dbForce.value,
    allow_same_driver: sameDriver.value
  }
}

async function testDatabaseTarget(): Promise<void> {
  dbTesting.value = true
  dbTargetMessage.value = ''
  dbTargetError.value = false
  try {
    if (!(await validateDatabaseTarget())) return
    const result = await api.post<Record<string, unknown>>('/upgrade/database/test', databaseTargetBody())
    dbTargetMessage.value = t('upgrade.database.testOk', {
      version: result?.server_version || result?.driver || '',
      empty: result?.empty ? t('common.yes') : t('common.no')
    })
  } catch (error) {
    dbTargetError.value = true
    dbTargetMessage.value = errorText(error)
  } finally {
    dbTesting.value = false
  }
}

async function preflightDatabaseConvert(): Promise<void> {
  dbPreflighting.value = true
  dbTargetMessage.value = ''
  dbTargetError.value = false
  try {
    if (!(await validateDatabaseTarget())) return
    dbPreflight.value = await api.post<DatabasePreflightReport>('/upgrade/database/preflight', databaseTargetBody())
  } catch (error) {
    dbTargetError.value = true
    dbTargetMessage.value = errorText(error)
  } finally {
    dbPreflighting.value = false
  }
}

async function startDatabaseConvert(): Promise<void> {
  dbStarting.value = true
  dbTargetMessage.value = ''
  dbTargetError.value = false
  try {
    if (!(await validateDatabaseTarget())) return
    if (!verifyMathChallenge()) return
    await api.post('/upgrade/database/convert', {
      ...databaseTargetBody(),
      account_password: dbPassword.value,
      totp: dbTOTP.value,
      // 服务端仍以 CONVERT 作为接口级确认词，界面层用算术验证替代手动输入。
      confirm: 'CONVERT'
    })
    dbPassword.value = ''
    dbTOTP.value = ''
    newMathChallenge()
    ElMessage.success(t('upgrade.database.started'))
    await loadDatabaseStatus()
    startDatabasePolling()
  } catch (error) {
    dbTargetError.value = true
    dbTargetMessage.value = errorText(error)
    // 操作区固定在底部，错误提示在滚动区，额外用 toast 保证立即可见。
    ElMessage.error(errorText(error))
  } finally {
    dbStarting.value = false
  }
}

async function rollbackDatabaseConvert(): Promise<void> {
  dbRollingBack.value = true
  try {
    if (!verifyRollbackMathChallenge()) return
    await api.post('/upgrade/database/rollback', {
      account_password: dbPassword.value,
      totp: dbTOTP.value,
      // 服务端仍以 ROLLBACK 作为接口级确认词，界面层用算术验证替代手动输入。
      confirm: 'ROLLBACK'
    })
    dbPassword.value = ''
    dbTOTP.value = ''
    newRollbackMathChallenge()
    ElMessage.success(t('upgrade.database.rollbackStarted'))
    await loadDatabaseStatus()
    startDatabasePolling()
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    dbRollingBack.value = false
  }
}

async function cancelDatabaseConvert(): Promise<void> {
  try {
    await api.post('/upgrade/database/convert/cancel', {})
    ElMessage.success(t('upgrade.database.cancelled'))
    await loadDatabaseStatus()
  } catch (error) {
    ElMessage.error(errorText(error))
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    status.value = await api.get<UpgradeStatus>('/upgrade')
    if (status.value) {
      vacuum.value = status.value.vacuum_allowed
    }
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    loading.value = false
  }
  await Promise.all([loadProgram(), loadDatabaseStatus(), loadUser().catch(() => null)])
  pickInitialTab()
}

// 首次进入时跳到最需要关注的分区；之后刷新保持用户当前所在的标签页。
function pickInitialTab(): void {
  if (initialTabPicked) return
  initialTabPicked = true
  if (dbStatus.value?.running) {
    tab.value = 'database'
  } else if (status.value?.needs_upgrade) {
    tab.value = 'maintenance'
  } else if (program.value?.restart_pending || program.value?.update_available) {
    tab.value = 'program'
  }
}

// 程序升级依赖 GitHub，网络失败不应影响数据迁移预检的展示。
async function loadProgram(): Promise<void> {
  try {
    program.value = await api.get<ProgramStatus>('/upgrade/program')
  } catch (_) {
    program.value = null
  }
}

async function checkProgram(): Promise<void> {
  programChecking.value = true
  try {
    program.value = await api.post<ProgramStatus>('/upgrade/program/check')
    if (program.value?.check_error) {
      ElMessage.warning(program.value.check_error)
    } else if (program.value?.update_available) {
      ElMessage.success(t('upgrade.program.found', { version: program.value.latest?.version }))
    } else {
      ElMessage.success(t('upgrade.program.latestMessage'))
    }
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    programChecking.value = false
  }
}

async function applyProgram(): Promise<void> {
  const target = program.value?.latest
  if (!target) return
  const confirmed = await ElMessageBox.confirm(
    t('upgrade.program.applyConfirm', { version: target.version, size: formatBytes(target.asset.size) }),
    t('upgrade.program.updateTo', { version: target.version }),
    {
      type: 'warning',
      confirmButtonText: t('upgrade.program.updateTo', { version: target.version }),
      cancelButtonText: t('common.cancel')
    }
  ).catch(() => false)
  if (!confirmed) return
  // 替换当前二进制属于敏感操作，后端要求浏览器会话再次认证。
  await loadUser().catch(() => null)
  programReauth.password = ''
  programReauth.totp = ''
  programPendingVersion.value = target.version
  programReauthVisible.value = true
}

async function confirmApplyProgram(): Promise<void> {
  const version = programPendingVersion.value
  if (!version) return
  if (!programReauth.password) {
    ElMessage.warning(t('settings.reauthPasswordRequired'))
    return
  }
  if (state.user?.totp_enabled && !/^\d{6}$/.test(programReauth.totp.trim())) {
    ElMessage.warning(t('settings.reauthTotpRequired'))
    return
  }
  programApplying.value = true
  try {
    const result = await api.postSlow<ProgramRunReport>('/upgrade/program/apply', {
      version,
      password: programReauth.password,
      totp: programReauth.totp.trim()
    })
    if (!result) return
    programReauthVisible.value = false
    programReauth.password = ''
    programReauth.totp = ''
    ElMessage.success(t('upgrade.program.applySuccess', { version: result.to_version }))
    await loadProgram()
    if (!result.restart_required) return
    const restart = await ElMessageBox.confirm(
      t('upgrade.program.restartConfirm', { version: result.to_version }),
      t('upgrade.program.restartTitle'),
      {
        type: 'success',
        confirmButtonText: t('upgrade.program.restartNow'),
        cancelButtonText: t('upgrade.program.restartLater')
      }
    ).catch(() => false)
    if (restart) await doRestart()
  } catch (error) {
    ElMessage.error(errorText(error))
    await loadProgram()
  } finally {
    programApplying.value = false
  }
}

async function restartProgram(): Promise<void> {
  const version = program.value?.last_run?.to_version || program.value?.latest?.version || ''
  const confirmed = await ElMessageBox.confirm(
    t('upgrade.program.restartConfirm', { version }),
    t('upgrade.program.restartTitle'),
    {
      type: 'warning',
      confirmButtonText: t('upgrade.program.restartNow'),
      cancelButtonText: t('common.cancel')
    }
  ).catch(() => false)
  if (confirmed) await doRestart()
}

async function doRestart(): Promise<void> {
  programRestarting.value = true
  try {
    const result = await api.post('/upgrade/program/restart', {})
    if (result === null) {
      programRestarting.value = false
      return
    }
    ElMessage.success(t('upgrade.program.restarting'))
    // 等待旧进程关闭后再探测；任意 HTTP 响应都说明新进程已经开始监听。
    await new Promise((resolve) => setTimeout(resolve, 3000))
    await waitForRestart()
  } catch (error) {
    ElMessage.error(errorText(error))
    programRestarting.value = false
  }
}

async function waitForRestart(): Promise<void> {
  const deadline = Date.now() + 60_000
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${apiBase}/auth/me`, { credentials: 'same-origin', cache: 'no-store' })
      if (response.status) {
        // 重启完成后刷新页面；开启会话保持时原登录状态仍然有效。
        window.location.assign('/')
        return
      }
    } catch (_) {
      // 服务尚未恢复，继续等待。
    }
    await new Promise((resolve) => setTimeout(resolve, 1000))
  }
  window.location.assign('/')
}

async function runAll(): Promise<void> {
  const confirmed = await ElMessageBox.confirm(t('upgrade.runConfirm'), t('upgrade.runAll'), {
    type: 'warning',
    confirmButtonText: t('upgrade.runAll'),
    cancelButtonText: t('common.cancel')
  }).catch(() => false)
  if (!confirmed) return
  running.value = true
  try {
    const result = await api.post<UpgradeRunReport>('/upgrade/run', { vacuum: vacuum.value })
    if (!result) return
    report.value = result
    ElMessage.success(t('upgrade.runSuccess'))
    await load()
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    running.value = false
  }
}

async function optimize(): Promise<void> {
  optimizing.value = true
  try {
    const result = await api.post<UpgradeRunReport>('/upgrade/optimize', { vacuum: vacuum.value, analyze: true })
    if (!result) return
    report.value = result
    ElMessage.success(t('upgrade.optimizeSuccess'))
    await load()
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    optimizing.value = false
  }
}

function formatBytes(bytes?: number): string {
  const value = Number(bytes || 0)
  if (value < 1024) return `${value} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let size = value / 1024
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index++
  }
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[index]}`
}

onMounted(load)
onUnmounted(stopDatabasePolling)
</script>

<style scoped>
.upgrade-tabs :deep(.el-tabs__header) {
  margin-bottom: 20px;
}

/* 五个标签页共用同一套结构：卡片固定高度，头部与汇总区固定，内容区滚动，操作区固定。 */
.upgrade-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 16px;
}

.maintenance-summary {
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
}

.database-grid {
  display: grid;
  grid-template-columns: minmax(180px, 0.6fr) minmax(280px, 1.4fr) auto;
  gap: 14px;
  align-items: start;
}

.database-force {
  padding-bottom: 6px;
}

.upgrade-card {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

/* 配置项表格卡片去掉下、左、右内边距，表格铺满卡片宽度并贴住底边。 */
.upgrade-table-card {
  padding: 22px 0 0;
  overflow: hidden;
}

/* 标题区单独保留左右内边距，保持与其他标签页的标题对齐。 */
.upgrade-table-card > .card-head {
  padding: 0 20px;
}

/* 配置项表格单元格左右内边距与其它配置表格保持一致。 */
.upgrade-table-card :deep(.el-table .cell) {
  padding: 0 20px;
}

.upgrade-card > .card-head,
.upgrade-card > .upgrade-summary,
.upgrade-card > .database-grid {
  flex: none;
}

.upgrade-scroll {
  min-height: 0;
}

.upgrade-warnings {
  flex: none;
  margin-top: 12px;
}

.upgrade-warnings :deep(.el-alert + .el-alert) {
  margin-top: 8px;
}

.upgrade-warnings :deep(.el-alert) {
  margin-bottom: 0;
}

.upgrade-footer {
  flex: none;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--kp-border);
}

.upgrade-status {
  margin-bottom: 12px;
}

.upgrade-head-state {
  margin: 0;
  max-width: 420px;
  text-align: right;
}

.status-line-alert {
  min-height: 28px;
  padding: 3px 10px;
}

.status-line-alert :deep(.el-alert__content) {
  min-width: 0;
}

.status-line-alert :deep(.el-alert__title) {
  display: block;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.status-line-alert :deep(.el-alert__icon) {
  margin-right: 6px;
}

.upgrade-progress .progress-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.progress-meta .stat-label {
  margin-bottom: 0;
}

.progress-percent {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
}

.footer-actions {
  /* 与“开始切换/一键回滚”操作组同宽，形成两列对齐的 2×2 结构。 */
  --footer-cell-width: 258px;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-top: 10px;
}

/* 账户组与操作组使用相同的两列结构，整体参与换行。 */
.footer-auth-pair,
.footer-action-pair {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 10px;
  flex-wrap: nowrap;
  min-width: 0;
}

.footer-action-group {
  display: flex;
  flex: 0 0 var(--footer-cell-width);
  width: var(--footer-cell-width);
  align-items: center;
  gap: 10px;
  min-width: 0;
}

/* 程序升级、升级优化等操作组不需要与认证输入框对齐，按内容宽度排列即可。 */
.footer-actions.is-auto {
  --footer-cell-width: auto;
}

.footer-actions.is-auto .footer-action-group {
  flex: 0 0 auto;
  width: auto;
}

.footer-auth-pair :deep(.el-input) {
  width: 100%;
  max-width: none;
}

@media (max-width: 620px) {
  .footer-actions {
    --footer-cell-width: 100%;
  }

  .footer-auth-pair,
  .footer-action-pair {
    flex: 1 1 100%;
    width: 100%;
    flex-wrap: wrap;
  }

  .footer-action-group {
    flex: 1 1 100%;
    width: 100%;
  }

  .footer-actions.is-auto .footer-action-group {
    flex: 1 1 100%;
    width: 100%;
  }
}



.math-check {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
}

.math-question {
  font-size: 13px;
  font-weight: 600;
  color: #475569;
  font-variant-numeric: tabular-nums;
}

.math-check.is-error .math-question {
  color: #dc2626;
}

.dsn-block {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed #eef1f6;
}

/* 每行内部标签与输入框左右横向排列，字段本身最多两列。 */
.dsn-form {
  max-width: 980px;
}

.dsn-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 360px), 1fr));
  gap: 0 24px;
  width: 100%;
}

.dsn-form :deep(.el-form-item) {
  /* 紧凑行距；错误提示缩小到 11px，仍能在 16px 内完整显示。 */
  margin-bottom: 16px;
}

.dsn-form :deep(.el-form-item__label) {
  height: 28px;
  line-height: 28px;
  white-space: nowrap;
}

.dsn-form :deep(.el-form-item:last-child) {
  margin-bottom: 0;
}

.dsn-form :deep(.el-input__wrapper),
.dsn-form :deep(.el-select__wrapper) {
  height: 28px;
}

.dsn-form :deep(.el-form-item__error) {
  padding-top: 2px;
  font-size: 11px;
  line-height: 1.2;
}

.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 2px;
  line-height: 1.6;
}

.dsn-form .form-help {
  margin-top: 10px;
  font-size: 11px;
  line-height: 1.5;
}

/* 说明文字与预览对齐到输入框列，而不是从标签列开始。 */
.dsn-form .form-help,
.dsn-form .dsn-preview {
  margin-left: 96px;
}

.dsn-preview {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 6px;
  min-height: 32px;
  padding: 5px 8px;
  background: #f8fafc;
  border-radius: 8px;
}

.dsn-preview .stat-label {
  margin-bottom: 0;
}

/* 预览固定单行高度，长 DSN 横向滚动，不把卡片撑高。 */
.dsn-preview code {
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  white-space: nowrap;
}

@media (min-width: 861px) {
  /* 所有标签页占满主内容区高度：头部固定，中间内容滚动，底部操作栏固定。 */
  .page.upgrade-page {
    display: flex;
    flex-direction: column;
    height: calc(100vh - var(--kp-topbar-h) - 41px);
    min-height: 460px;
  }

  .upgrade-page .upgrade-tabs {
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .upgrade-page .upgrade-tabs :deep(.el-tabs__content) {
    flex: 1 1 auto;
    min-height: 0;
    overflow: hidden;
  }

  .upgrade-page .upgrade-tabs :deep(.el-tab-pane) {
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }

  .upgrade-card {
    height: 100%;
    margin-bottom: 0;
  }

  .upgrade-scroll {
    flex: 1 1 auto;
    overflow-y: auto;
    padding-right: 4px;
  }

  /* 配置项表格铺满内容区，横向滚动条贴在内容区底部，而不是紧跟最后一行。 */
  .upgrade-scroll > .table-scroll {
    height: 100%;
  }
}

@media (max-width: 860px) {
  .database-grid {
    grid-template-columns: 1fr;
  }

  .card-head {
    flex-wrap: wrap;
  }

  .upgrade-head-state {
    max-width: none;
    text-align: left;
  }
}

.program-grid {
  margin-top: 18px;
}

/* 程序升级详情区与汇总区之间留出统一间距，内容过长时只在详情区滚动。 */
.program-detail > * + * {
  margin-top: 12px;
}

.result-block {
  margin-top: 4px;
  padding-top: 16px;
  border-top: 1px solid var(--kp-border);
}

.result-head {
  margin-bottom: 10px;
}

.result-head h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}

.result-head .sub {
  color: var(--kp-muted);
  font-size: 12px;
  margin-top: 4px;
}

.program-value {
  font-size: 16px;
  font-weight: 600;
  color: #0f172a;
}

.release-notes {
  margin: 0;
  padding: 12px 16px;
  max-height: 320px;
  overflow: auto;
  background: #f8fafc;
  border-radius: 8px;
  color: #475569;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
}

.stat-label {
  color: #64748b;
  font-size: 13px;
  margin-bottom: 8px;
}

.stat-value {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.text-green {
  color: #16a34a;
}

.text-red {
  color: #dc2626;
}

.action-list {
  margin: 12px 0 0;
  padding-left: 20px;
  color: #475569;
  line-height: 1.9;
}
</style>
