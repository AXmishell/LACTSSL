<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('upgrade.title') }}</h1>
        <p>{{ t('upgrade.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle :loading="loading" @click="load" />
      </div>
    </div>

    <div v-if="program" class="k-card" style="margin-bottom: 16px">
      <div class="card-head">
        <div>
          <h3>{{ t('upgrade.program.title') }}</h3>
          <div class="sub">
            {{ t('upgrade.program.subtitle', { repo: program.repo, channel: program.channel }) }}
          </div>
        </div>
        <div class="flex gap-10 wrap" style="align-items: center">
          <el-tag effect="plain" round size="small">{{ program.platform }}</el-tag>
          <el-button
            :icon="RefreshRight"
            :loading="programChecking"
            :disabled="!program.enabled"
            @click="checkProgram"
          >
            {{ t('upgrade.program.check') }}
          </el-button>
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
            v-else-if="program.can_update"
            type="primary"
            :icon="Download"
            :loading="programApplying"
            @click="applyProgram"
          >
            {{ t('upgrade.program.updateTo', { version: program.latest?.version }) }}
          </el-button>
        </div>
      </div>

      <div class="program-grid">
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

      <el-alert
        v-if="program.check_error"
        type="error"
        :closable="false"
        show-icon
        :title="t('upgrade.program.checkFailed', { error: program.check_error })"
        style="margin-top: 12px"
      />
      <el-alert
        v-else-if="program.restart_pending"
        type="success"
        :closable="false"
        show-icon
        :title="t('upgrade.program.restartPending', { version: program.last_run?.to_version || program.latest?.version })"
        :description="program.restart_mode === 'exit' ? t('upgrade.program.restartExitHint') : t('upgrade.program.restartHint')"
        style="margin-top: 12px"
      />
      <el-alert
        v-else-if="program.reason && !program.can_update"
        :type="program.update_available ? 'warning' : 'info'"
        :closable="false"
        show-icon
        :title="program.reason"
        style="margin-top: 12px"
      />

      <el-collapse v-if="program.latest?.notes" style="margin-top: 12px">
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
      <div v-if="program.last_run" class="muted" style="margin-top: 12px">
        {{ t('upgrade.program.lastRun', {
          from: program.last_run.from_version,
          to: program.last_run.to_version,
          time: formatDate(program.last_run.finished_at)
        }) }}
        <span v-if="program.last_run.backup_path" class="mono break-all"> · {{ program.last_run.backup_path }}</span>
      </div>
    </div>

    <div class="k-card" style="margin-bottom: 16px">
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
            <el-option value="sqlite" label="SQLite" />
          </el-select>
        </div>
        <div v-if="dbTargetDriver === 'postgres'">
          <div class="stat-label">{{ t('upgrade.database.dsn') }}</div>
          <el-input v-model="dbTargetDSN" class="mono" placeholder="postgres://user:pass@host:5432/db?sslmode=verify-full" :disabled="dbStatus?.running" />
        </div>
        <div v-else>
          <div class="stat-label">{{ t('upgrade.database.path') }}</div>
          <el-input v-model="dbTargetPath" class="mono" placeholder="data/lactssl-converted.db" :disabled="dbStatus?.running" />
        </div>
        <div class="database-force">
          <el-checkbox v-model="dbForce" :disabled="dbStatus?.running">
            {{ t('upgrade.database.force') }}
          </el-checkbox>
        </div>
      </div>

      <div class="flex gap-10 wrap" style="margin-top: 14px">
        <el-button :loading="dbTesting" :disabled="dbStatus?.running" @click="testDatabaseTarget">
          {{ t('upgrade.database.test') }}
        </el-button>
        <el-button :loading="dbPreflighting" :disabled="dbStatus?.running" @click="preflightDatabaseConvert">
          {{ t('upgrade.database.preflight') }}
        </el-button>
      </div>

      <el-alert
        v-if="dbTargetMessage"
        :type="dbTargetError ? 'error' : 'success'"
        :closable="false"
        show-icon
        :title="dbTargetMessage"
        style="margin-top: 12px"
      />
      <div v-if="dbPreflight" class="muted" style="margin-top: 10px">
        {{ t('upgrade.database.preflightSummary', {
          tables: dbPreflight.source_tables,
          rows: dbPreflight.source_rows,
          size: formatBytes(dbPreflight.source_bytes),
          empty: dbPreflight.target_empty ? t('common.yes') : t('common.no')
        }) }}
      </div>

      <el-collapse v-if="dbStatus?.state || dbStatus?.progress" style="margin-top: 12px">
        <el-collapse-item :title="t('upgrade.database.progress')" name="progress">
          <div class="muted">
            {{ t('upgrade.database.phaseLabel', { phase: dbProgressLabel }) }}
            <span v-if="dbStatus?.progress.table"> · {{ dbStatus.progress.table }}</span>
            <span v-if="dbStatus?.progress.table_total">
              ({{ dbStatus.progress.table_index }}/{{ dbStatus.progress.table_total }})
            </span>
          </div>
          <el-progress
            :percentage="dbPercent"
            :status="dbStatus?.state?.phase === 'failed' ? 'exception' : undefined"
            style="margin-top: 8px"
          />
          <div class="muted" style="margin-top: 6px">
            {{ t('upgrade.database.rows', {
              rows: dbStatus?.progress.rows || 0,
              total: dbStatus?.progress.total_rows || 0
            }) }}
          </div>
          <el-alert
            v-if="dbStatus?.state?.error"
            type="error"
            :closable="false"
            show-icon
            :title="dbStatus.state.error"
            style="margin-top: 8px"
          />
          <div v-if="dbStatus?.report?.backup_path" class="muted mono break-all" style="margin-top: 8px">
            {{ t('upgrade.database.backup') }}: {{ dbStatus.report.backup_path }}
          </div>
        </el-collapse-item>
      </el-collapse>

      <div style="margin-top: 14px">
        <el-input v-model="dbPassword" type="password" show-password :placeholder="t('upgrade.database.password')" style="max-width: 280px" />
        <el-input v-model="dbTOTP" :placeholder="t('upgrade.database.totp')" style="max-width: 180px; margin-left: 8px" />
        <el-input v-model="dbConfirm" :placeholder="t('upgrade.database.confirmPlaceholder')" style="max-width: 180px; margin-left: 8px" />
        <el-button type="primary" :loading="dbStarting" :disabled="dbStatus?.running" style="margin-left: 8px" @click="startDatabaseConvert">
          {{ t('upgrade.database.start') }}
        </el-button>
        <el-button v-if="dbStatus?.running" @click="cancelDatabaseConvert">
          {{ t('common.cancel') }}
        </el-button>
      </div>
      <div v-if="dbStatus?.state?.phase === 'completed'" style="margin-top: 10px">
        <el-input v-model="dbRollbackConfirm" :placeholder="t('upgrade.database.rollbackPlaceholder')" style="max-width: 180px" />
        <el-button type="danger" plain :loading="dbRollingBack" style="margin-left: 8px" @click="rollbackDatabaseConvert">
          {{ t('upgrade.database.rollback') }}
        </el-button>
      </div>
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        :title="t('upgrade.database.warning')"
        style="margin-top: 12px"
      />
    </div>

    <el-alert
      v-if="status && status.needs_upgrade"
      type="warning"
      :closable="false"
      show-icon
      :title="t('upgrade.pendingTitle')"
      :description="t('upgrade.pendingDesc', { stored: status.stored_version, current: status.current_version })"
      style="margin-bottom: 16px"
    />
    <el-alert
      v-else-if="status"
      type="success"
      :closable="false"
      show-icon
      :title="t('upgrade.upToDateTitle')"
      :description="t('upgrade.upToDateDesc', { version: status.current_version })"
      style="margin-bottom: 16px"
    />

    <div v-if="status" class="upgrade-grid">
      <div class="k-card stat-card">
        <div class="stat-label">{{ t('upgrade.appVersion') }}</div>
        <div class="stat-value">{{ status.app_version || t('common.none') }}</div>
        <div class="muted">{{ t('upgrade.checkedAt', { time: formatDate(status.checked_at) }) }}</div>
      </div>
      <div class="k-card stat-card">
        <div class="stat-label">{{ t('upgrade.dataVersion') }}</div>
        <div class="stat-value">v{{ status.stored_version }} → v{{ status.current_version }}</div>
        <div class="muted">{{ status.version_label }}</div>
      </div>
      <div class="k-card stat-card">
        <div class="stat-label">{{ t('upgrade.databaseSize') }}</div>
        <div class="stat-value">{{ formatBytes(status.database.file_size + status.database.wal_size) }}</div>
        <div class="muted">{{ t('upgrade.freePages', { size: formatBytes(status.database.free_bytes) }) }}</div>
      </div>
      <div class="k-card stat-card">
        <div class="stat-label">{{ t('upgrade.integrity') }}</div>
        <div class="stat-value" :class="status.database.integrity === 'ok' ? 'text-green' : 'text-red'">
          {{ status.database.integrity }}
        </div>
        <div class="muted">
          {{ t('upgrade.dbMeta', { tables: status.database.tables, indexes: status.database.indexes, mode: status.database.journal_mode }) }}
        </div>
      </div>
    </div>

    <div v-if="status" class="k-card" style="margin-top: 16px">
      <div class="card-head">
        <div>
          <h3>{{ t('upgrade.actionsTitle') }}</h3>
          <div class="sub">{{ t('upgrade.actionsSub') }}</div>
        </div>
        <div class="flex gap-10 wrap" style="align-items: center">
          <el-switch v-model="vacuum" :disabled="!status.vacuum_allowed" :active-text="t('upgrade.vacuum')" />
          <el-button :icon="Refresh" :loading="optimizing" @click="optimize">{{ t('upgrade.optimizeOnly') }}</el-button>
          <el-button type="primary" :icon="MagicStick" :loading="running" @click="runAll">{{ t('upgrade.runAll') }}</el-button>
        </div>
      </div>
      <div class="form-help">
        {{ t('upgrade.autoState', { state: status.auto_enabled ? t('common.enabled') : t('common.disabled') }) }}
        · {{ t('upgrade.vacuumState', { state: status.vacuum_allowed ? t('common.enabled') : t('common.disabled') }) }}
      </div>
      <el-alert
        v-for="warning in status.warnings"
        :key="warning"
        type="warning"
        :closable="false"
        show-icon
        :title="warning"
        style="margin-top: 8px"
      />
    </div>

    <div v-if="report" class="k-card" style="margin-top: 16px">
      <div class="card-head">
        <div>
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

    <div v-if="status" class="k-card" style="margin-top: 16px; padding: 24px">
      <div class="card-head">
        <div>
          <h3>{{ t('upgrade.configTitle') }}</h3>
          <div class="sub">{{ t('upgrade.configSub', { path: status.config.path || t('common.none'), count: status.config.changes.length }) }}</div>
        </div>
      </div>
      <div class="table-scroll">
        <el-table :data="status.config.changes" style="width: 100%">
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

    <div v-if="status?.last_run" class="k-card" style="margin-top: 16px">
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
      <el-alert v-if="status.last_run.error" type="error" :closable="false" show-icon :title="status.last_run.error" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Download, MagicStick, Refresh, RefreshRight, SwitchButton } from '@element-plus/icons-vue'
import { api, apiBase, errorText } from '../api'
import { t } from '../i18n'
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
const running = ref(false)
const optimizing = ref(false)
const vacuum = ref(true)
const program = ref<ProgramStatus | null>(null)
const programChecking = ref(false)
const programApplying = ref(false)
const programRestarting = ref(false)
const dbStatus = ref<DatabaseConvertStatus | null>(null)
const dbTargetDriver = ref('postgres')
const dbTargetPath = ref('')
const dbTargetDSN = ref('')
const dbForce = ref(false)
const dbTesting = ref(false)
const dbPreflighting = ref(false)
const dbStarting = ref(false)
const dbPreflight = ref<DatabasePreflightReport | null>(null)
const dbTargetMessage = ref('')
const dbTargetError = ref(false)
const dbPassword = ref('')
const dbTOTP = ref('')
const dbConfirm = ref('')
const dbRollbackConfirm = ref('')
const dbRollingBack = ref(false)
let dbPollTimer: number | undefined

const dbProgressLabel = computed(() => t(`upgrade.database.phase.${dbStatus.value?.state?.phase || dbStatus.value?.progress.phase || 'idle'}`))
const dbPercent = computed(() => {
  const progress = dbStatus.value?.progress
  if (!progress) return 0
  if (progress.total_rows > 0) return Math.min(100, Math.round((progress.rows / progress.total_rows) * 100))
  if (progress.table_total > 0) return Math.min(100, Math.round((progress.table_index / progress.table_total) * 100))
  return 0
})

async function loadDatabaseStatus(): Promise<void> {
  try {
    dbStatus.value = await api.get<DatabaseConvertStatus>('/upgrade/database')
    if (dbStatus.value.source.driver === 'postgres') {
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
    target_dsn: dbTargetDSN.value,
    force: dbForce.value
  }
}

async function testDatabaseTarget(): Promise<void> {
  dbTesting.value = true
  dbTargetMessage.value = ''
  dbTargetError.value = false
  try {
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
    await api.post('/upgrade/database/convert', {
      ...databaseTargetBody(),
      account_password: dbPassword.value,
      totp: dbTOTP.value,
      confirm: dbConfirm.value
    })
    dbPassword.value = ''
    dbTOTP.value = ''
    dbConfirm.value = ''
    ElMessage.success(t('upgrade.database.started'))
    await loadDatabaseStatus()
    startDatabasePolling()
  } catch (error) {
    dbTargetError.value = true
    dbTargetMessage.value = errorText(error)
  } finally {
    dbStarting.value = false
  }
}

async function rollbackDatabaseConvert(): Promise<void> {
  dbRollingBack.value = true
  try {
    await api.post('/upgrade/database/rollback', {
      account_password: dbPassword.value,
      totp: dbTOTP.value,
      confirm: dbRollbackConfirm.value
    })
    dbPassword.value = ''
    dbTOTP.value = ''
    dbRollbackConfirm.value = ''
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
  await Promise.all([loadProgram(), loadDatabaseStatus()])
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
  programApplying.value = true
  try {
    const result = await api.postSlow<ProgramRunReport>('/upgrade/program/apply', { version: target.version })
    if (!result) return
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
.upgrade-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
}

.database-grid {
  display: grid;
  grid-template-columns: minmax(180px, 0.6fr) minmax(280px, 1.4fr) auto;
  gap: 14px;
  align-items: end;
}

.database-force {
  padding-bottom: 6px;
}

@media (max-width: 860px) {
  .database-grid {
    grid-template-columns: 1fr;
  }
}

.program-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 16px;
  margin-top: 18px;
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

.stat-card {
  padding: 20px 22px;
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
