<template>
  <div class="page table-page">
    <div class="page-head">
      <div>
        <h1>{{ t('logs.title') }}</h1>
      </div>
      <el-tabs v-model="tab" class="logs-tabs" @tab-change="onTabChange">
        <el-tab-pane :label="t('logs.tabOperation')" name="operation" />
        <el-tab-pane :label="t('logs.tabSystem')" name="system" />
        <el-tab-pane :label="t('logs.tabNotify')" name="notify" />
        <el-tab-pane :label="t('logs.tabArchive')" name="archive" />
      </el-tabs>
    </div>

    <div class="page-actions logs-actions">
      <template v-if="tab === 'operation'">
        <el-select v-model="source" :placeholder="t('logs.allSources')" style="width: 140px" @change="reload">
          <el-option v-for="item in sources" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
        <el-select v-model="level" :placeholder="t('logs.allLevels')" style="width: 140px" @change="reload">
          <el-option v-for="item in levels" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
      </template>
      <template v-else-if="tab === 'system'">
        <el-select v-model="systemLevel" :placeholder="t('systemLogs.allLevels')" style="width: 140px" @change="reload">
          <el-option v-for="item in systemLevels" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
        <el-select v-model="component" clearable :placeholder="t('systemLogs.allComponents')" style="width: 140px" @change="reload">
          <el-option v-for="item in components" :key="item" :label="componentLabel(item)" :value="item" />
        </el-select>
      </template>
      <template v-else-if="tab === 'notify'">
        <el-select v-model="notifyLevel" :placeholder="t('logs.allLevels')" style="width: 140px" @change="reload">
          <el-option v-for="item in levels" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
      </template>
      <template v-else>
        <el-select v-model="archiveCategory" :placeholder="t('logs.archiveAllCategories')" style="width: 130px" @change="reload">
          <el-option value="" :label="t('logs.archiveAllCategories')" />
          <el-option value="operation" :label="t('logs.archiveCategoryOperation')" />
          <el-option value="runtime" :label="t('logs.archiveCategoryRuntime')" />
        </el-select>
        <el-select v-model="archiveReason" :placeholder="t('logs.archiveAllReasons')" style="width: 150px" @change="reload">
          <el-option value="" :label="t('logs.archiveAllReasons')" />
          <el-option value="manual_clear" :label="t('logs.archiveReasonManual')" />
          <el-option value="retention" :label="t('logs.archiveReasonRetention')" />
          <el-option value="max_rows" :label="t('logs.archiveReasonMaxRows')" />
        </el-select>
      </template>
      <el-input
        v-model="activeQuery"
        clearable
        maxlength="100"
        :placeholder="searchPlaceholder"
        style="width: 210px"
        @keyup.enter="reload"
        @clear="reload"
      >
        <template #suffix>
          <el-icon class="log-search" @click="reload"><Search /></el-icon>
        </template>
      </el-input>
      <el-switch v-model="autoRefresh" :active-text="t('logs.autoRefresh')" />
      <el-date-picker
        v-model="activeRange"
        type="daterange"
        unlink-panels
        range-separator="-"
        style="width: 240px"
        @change="reload"
      />
      <el-dropdown v-if="tab !== 'archive'" trigger="click" @command="exportLogs">
        <el-button :icon="Download" :loading="exporting" :title="t('logs.exportHint')">
          {{ exporting ? t('logs.exporting') : t('logs.export') }}
          <el-icon class="el-icon--right"><ArrowDown /></el-icon>
        </el-button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="csv">{{ t('logs.exportCsv') }}</el-dropdown-item>
            <el-dropdown-item command="json">{{ t('logs.exportJson') }}</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
      <el-button :icon="Refresh" circle @click="load()" />
      <el-button
        v-if="tab !== 'archive' && tab !== 'notify'"
        type="danger"
        plain
        :icon="Delete"
        :loading="clearing"
        @click="clearAll"
      >
        {{ t(tab === 'operation' ? 'logs.clear' : 'systemLogs.clear') }}
      </el-button>
    </div>

    <el-alert
      v-if="tab === 'system' && !enabled"
      :title="t('systemLogs.disabled')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
    />
    <el-alert
      v-if="tab === 'archive' && !archiveEnabled"
      :title="t('logs.archiveDisabled')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
    />
    <div v-if="tab === 'archive' && archiveEnabled && archiveDir" class="muted archive-dir">
      {{ t('logs.archiveDir') }}: <span class="mono">{{ archiveDir }}</span>
    </div>

    <div class="k-card table-fill" style="padding: 0; overflow: hidden">
      <div class="table-scroll">
        <el-table v-if="tab !== 'archive'" :data="items" v-loading="loading" row-key="id" height="100%" style="width: 100%">
          <el-table-column v-if="tab === 'system'" type="expand" width="48">
            <template #default="{ row }">
              <div class="system-log-fields">
                <template v-if="fieldEntries(row).length">
                  <div v-for="[key, value] in fieldEntries(row)" :key="key" class="system-log-field">
                    <span class="system-log-key mono">{{ key }}</span>
                    <pre class="system-log-value">{{ formatValue(value) }}</pre>
                  </div>
                </template>
                <span v-else class="muted">{{ t('systemLogs.noDetails') }}</span>
              </div>
            </template>
          </el-table-column>
          <!-- 操作日志没有展开列，占位 48px 保证切换页签时其余列不横移。 -->
          <el-table-column v-else width="48" />
          <el-table-column width="110" :label="t('logs.level')">
            <template #default="{ row }">
              <span class="tag-soft" :class="levelClass(row.level)">{{ t(levelKey(row.level)) }}</span>
            </template>
          </el-table-column>
          <el-table-column v-if="tab === 'operation'" width="130" :label="t('logs.source')">
            <template #default="{ row }">
              <span class="muted">{{ sourceLabel(row.source) }}</span>
            </template>
          </el-table-column>
          <el-table-column v-else-if="tab === 'system'" width="130" :label="t('systemLogs.component')">
            <template #default="{ row }">
              <span class="muted">{{ componentLabel(row.component) }}</span>
            </template>
          </el-table-column>
          <el-table-column
            min-width="380"
            :label="tab === 'system' ? t('systemLogs.message') : t('logs.content')"
          >
            <template #default="{ row }">
              <span class="log-message">{{ row.message }}</span>
            </template>
          </el-table-column>
          <el-table-column width="150" align="right" show-overflow-tooltip :label="t('logs.time')">
            <template #default="{ row }">
              <span class="muted">{{ formatDate(row.created_at) }}</span>
            </template>
          </el-table-column>
          <template #empty>
            <div style="padding: 40px 0; color: #94a3b8">
              {{
                tab === 'operation'
                  ? t('logs.empty')
                  : tab === 'system'
                    ? t('systemLogs.empty')
                    : t('logs.notifyEmpty')
              }}
            </div>
          </template>
        </el-table>
        <el-table v-else :data="archiveItems" v-loading="loading" row-key="archive_id" height="100%" style="width: 100%">
          <el-table-column width="170" align="right" :label="t('logs.archiveCreatedAt')">
            <template #default="{ row }">
              <div class="muted">{{ formatDate(row.created_at) }}</div>
              <div class="archive-file mono" :title="row.file?.name">
                {{ archiveFileName(row) }}
                <el-tag v-if="row.missing" size="small" type="warning">{{ t('logs.archiveMissing') }}</el-tag>
              </div>
            </template>
          </el-table-column>
          <el-table-column width="110" :label="t('logs.archiveCategory')">
            <template #default="{ row }">
              <span class="tag-soft tag-blue">{{ archiveCategoryLabel(row.category) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="140" :label="t('logs.archiveReason')">
            <template #default="{ row }">
              <span class="muted">{{ archiveReasonLabel(row.reason) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="90" align="right" :label="t('logs.archiveRows')">
            <template #default="{ row }">
              <span class="mono">{{ row.row_count }}</span>
            </template>
          </el-table-column>
          <el-table-column min-width="230" :label="t('logs.archiveRange')">
            <template #default="{ row }">
              <span class="muted">{{ archiveRangeText(row) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="100" align="right" :label="t('logs.archiveFileSize')">
            <template #default="{ row }">
              <span class="muted">{{ formatBytes(row.file?.bytes || 0) }}</span>
            </template>
          </el-table-column>
          <el-table-column min-width="150" show-overflow-tooltip :label="t('logs.archiveOperator')">
            <template #default="{ row }">
              <span class="muted">{{ archiveOperator(row) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="150" align="right" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" :disabled="row.missing" @click="downloadArchive(row)">
                {{ t('logs.archiveDownload') }}
              </el-button>
              <el-button link type="danger" @click="deleteArchive(row)">
                {{ t('logs.archiveDelete') }}
              </el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div style="padding: 40px 0; color: #94a3b8">
              {{ t('logs.archiveEmpty') }}
            </div>
          </template>
        </el-table>
      </div>
      <div class="flex-between logs-foot">
        <span class="muted">
          {{ tab === 'archive' ? t('logs.archiveTotal', { total: archiveTotal }) : t('logs.total', { total }) }}
        </span>
        <el-pagination
          v-if="tab === 'archive'"
          v-model:current-page="archivePage"
          :page-size="archiveSize"
          :total="archiveTotal"
          layout="prev, pager, next"
          background
          @current-change="load()"
        />
        <el-pagination
          v-else
          v-model:current-page="page"
          :page-size="size"
          :total="total"
          layout="prev, pager, next"
          background
          @current-change="load()"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, Delete, Download, Refresh, Search } from '@element-plus/icons-vue'
import { api, downloadFile } from '../api'
import { formatDate } from '../utils'
import type { LogArchiveEntry, LogArchivePage, LogPage, SystemLogPage } from '../types'

interface LogRow {
  id: number
  level: string
  message: string
  created_at: string
  source?: string
  component?: string
  fields?: Record<string, unknown> | null
}

const route = useRoute()
const { t } = useI18n()
const tab = ref(route.meta.logTab === 'system' ? 'system' : route.meta.logTab === 'notify' ? 'notify' : 'operation')
const loading = ref(false)
const clearing = ref(false)
const exporting = ref(false)
const items = ref<LogRow[]>([])
const total = ref(0)
const page = ref(1)
const size = 30

// 操作日志筛选
const source = ref('')
const level = ref('')
const operationQuery = ref('')
const operationRange = ref<[Date, Date] | null>(null)

// 系统日志筛选
const systemLevel = ref('')
const component = ref('')
const systemQuery = ref('')
const systemRange = ref<[Date, Date] | null>(null)
const components = ref<string[]>([])
const enabled = ref(true)
const autoRefresh = ref(true)

// 通知日志复用操作日志中 source=notify 的记录，独立保存筛选状态。
const notifyLevel = ref('')
const notifyQuery = ref('')
const notifyRange = ref<[Date, Date] | null>(null)

// 归档筛选与分页独立，避免和操作/系统/通知日志的状态互相影响。
const archiveItems = ref<LogArchiveEntry[]>([])
const archiveTotal = ref(0)
const archivePage = ref(1)
const archiveSize = 20
const archiveCategory = ref('')
const archiveReason = ref('')
const archiveQuery = ref('')
const archiveRange = ref<[Date, Date] | null>(null)
const archiveEnabled = ref(true)
const archiveDir = ref('')

let timer: ReturnType<typeof setInterval> | null = null
let loadSeq = 0

const sources = [
  { value: '', label: 'logs.allSources' },
  { value: 'task', label: 'logs.sourceTask' },
  { value: 'deploy', label: 'logs.sourceDeploy' },
  { value: 'monitor', label: 'logs.sourceMonitor' },
  { value: 'auth', label: 'logs.sourceAuth' },
  { value: 'dns', label: 'logs.sourceDns' },
  { value: 'ca', label: 'logs.sourceCA' },
  { value: 'settings', label: 'logs.sourceSettings' },
  { value: 'notify', label: 'logs.sourceNotify' },
  { value: 'system', label: 'logs.sourceSystem' },
  { value: 'acme', label: 'logs.sourceACME' },
  { value: 'cert', label: 'logs.sourceCert' },
  { value: 'csr', label: 'logs.sourceCSR' },
  { value: 'apikey', label: 'logs.sourceAPIKeys' }
]

const levels = [
  { value: '', label: 'logs.allLevels' },
  { value: 'info', label: 'logs.info' },
  { value: 'success', label: 'logs.success' },
  { value: 'warning', label: 'logs.warning' },
  { value: 'error', label: 'logs.error' }
]

const systemLevels = [
  { value: '', label: 'systemLogs.allLevels' },
  { value: 'debug', label: 'systemLogs.levelDebug' },
  { value: 'info', label: 'systemLogs.levelInfo' },
  { value: 'warn', label: 'systemLogs.levelWarn' },
  { value: 'error', label: 'systemLogs.levelError' }
]

const componentKeys: Record<string, string> = {
  app: 'systemLogs.componentApp',
  main: 'systemLogs.componentMain',
  http: 'systemLogs.componentHTTP',
  service: 'systemLogs.componentService',
  acme: 'systemLogs.componentACME'
}

const activeRange = computed({
  get: () => {
    if (tab.value === 'archive') return archiveRange.value
    if (tab.value === 'operation') return operationRange.value
    if (tab.value === 'notify') return notifyRange.value
    return systemRange.value
  },
  set: (value: [Date, Date] | null) => {
    if (tab.value === 'archive') {
      archiveRange.value = value
    } else if (tab.value === 'operation') {
      operationRange.value = value
    } else if (tab.value === 'notify') {
      notifyRange.value = value
    } else {
      systemRange.value = value
    }
  }
})

// 关键字搜索各页签共用控件，但各自保留筛选状态。
const activeQuery = computed({
  get: () => {
    if (tab.value === 'archive') return archiveQuery.value
    if (tab.value === 'operation') return operationQuery.value
    if (tab.value === 'notify') return notifyQuery.value
    return systemQuery.value
  },
  set: (value: string) => {
    if (tab.value === 'archive') {
      archiveQuery.value = value
    } else if (tab.value === 'operation') {
      operationQuery.value = value
    } else if (tab.value === 'notify') {
      notifyQuery.value = value
    } else {
      systemQuery.value = value
    }
  }
})

const searchPlaceholder = computed(() => {
  if (tab.value === 'archive') return t('logs.archiveSearchPlaceholder')
  if (tab.value === 'operation') return t('logs.searchPlaceholder')
  if (tab.value === 'notify') return t('logs.notifySearchPlaceholder')
  return t('systemLogs.searchPlaceholder')
})

function sourceLabel(value?: string): string {
  if (!value) return ''
  const item = sources.find((entry) => entry.value === value)
  return item ? t(item.label) : value
}

function componentLabel(value?: string): string {
  if (!value) return ''
  const key = componentKeys[value]
  return key ? t(key) : value
}

function levelKey(value: string): string {
  const map: Record<string, string> = {
    debug: 'systemLogs.levelDebug',
    info: 'logs.info',
    success: 'logs.success',
    warning: 'logs.warning',
    warn: 'logs.warning',
    error: 'logs.error'
  }
  return map[value] || 'logs.info'
}

function levelClass(value: string): string {
  const map: Record<string, string> = {
    debug: 'tag-gray',
    info: 'tag-blue',
    success: 'tag-green',
    warning: 'tag-amber',
    warn: 'tag-amber',
    error: 'tag-red'
  }
  return map[value] || 'tag-gray'
}

// 列表与导出共用同一组筛选条件，避免“看到的”和“导出的”不一致。
function filterParams(): Record<string, string> {
  const params: Record<string, string> = {}
  const range = activeRange.value
  if (range && range.length === 2) {
    const [start, end] = range
    params.from = start.toISOString()
    // 结束日期按本地整天处理，to 为次日零点，后端使用左闭右开区间。
    const next = new Date(end.getFullYear(), end.getMonth(), end.getDate() + 1)
    params.to = next.toISOString()
  }
  if (tab.value === 'archive') {
    if (archiveCategory.value) params.category = archiveCategory.value
    if (archiveReason.value) params.reason = archiveReason.value
  } else if (tab.value === 'operation') {
    if (source.value) params.source = source.value
    if (level.value) params.level = level.value
  } else if (tab.value === 'notify') {
    // 通知日志是操作日志中固定来源为 notify 的视图，避免维护第二套存储。
    params.source = 'notify'
    if (notifyLevel.value) params.level = notifyLevel.value
  } else {
    if (systemLevel.value) params.level = systemLevel.value
    if (component.value) params.component = component.value
  }
  if (activeQuery.value.trim()) params.q = activeQuery.value.trim()
  return params
}

async function load(silent = false): Promise<void> {
  const seq = ++loadSeq
  if (!silent) loading.value = true
  try {
    if (tab.value === 'operation' || tab.value === 'notify') {
      const data = await api.get<LogPage>('/logs', { page: page.value, size, ...filterParams() })
      if (seq !== loadSeq) return
      items.value = data.items || []
      total.value = data.total || 0
    } else if (tab.value === 'system') {
      const data = await api.get<SystemLogPage>('/system-logs', {
        page: page.value,
        size,
        ...filterParams()
      })
      if (seq !== loadSeq) return
      items.value = data.items || []
      total.value = data.total || 0
      components.value = data.components || []
      enabled.value = data.enabled !== false
    } else {
      const data = await api.get<LogArchivePage>('/log-archives', {
        page: archivePage.value,
        size: archiveSize,
        ...filterParams()
      })
      if (seq !== loadSeq) return
      archiveItems.value = data.items || []
      archiveTotal.value = data.total || 0
      archiveEnabled.value = data.enabled !== false
      archiveDir.value = data.dir || ''
    }
  } catch (error: any) {
    if (seq === loadSeq && !silent) ElMessage.error(error.message)
  } finally {
    if (seq === loadSeq && !silent) loading.value = false
  }
}

function reload(): void {
  if (tab.value === 'archive') {
    archivePage.value = 1
  } else {
    page.value = 1
  }
  load()
}

function onTabChange(): void {
  void switchTab()
}

// 切页签时只重新拉取数据，加载动画交给表格的 v-loading，不做位移/淡出动画。
async function switchTab(): Promise<void> {
  page.value = 1
  archivePage.value = 1
  await load()
}

function fileStamp(): string {
  const now = new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
}

async function exportLogs(format: string): Promise<void> {
  if (exporting.value) return
  if (total.value === 0) {
    ElMessage.warning(
      tab.value === 'operation'
        ? t('logs.exportEmpty')
        : tab.value === 'notify'
          ? t('logs.notifyExportEmpty')
          : t('systemLogs.exportEmpty')
    )
    return
  }
  exporting.value = true
  try {
    const params = new URLSearchParams(filterParams())
    params.set('format', format)
    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (timezone) params.set('tz', timezone)
    const isSystem = tab.value === 'system'
    const endpoint = isSystem ? '/system-logs/export' : '/logs/export'
    const prefix = isSystem
      ? 'lactssl-system-logs'
      : tab.value === 'notify'
        ? 'lactssl-notify-logs'
        : 'lactssl-logs'
    await downloadFile(`${endpoint}?${params.toString()}`, `${prefix}-${fileStamp()}.${format}`)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    exporting.value = false
  }
}

function fieldEntries(row: LogRow): Array<[string, unknown]> {
  return Object.entries(row.fields || {}).sort(([left], [right]) => left.localeCompare(right))
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined) return '-'
  if (typeof value === 'object') return JSON.stringify(value, null, 2)
  return String(value)
}

function archiveCategoryLabel(value?: string): string {
  if (value === 'runtime') return t('logs.archiveCategoryRuntime')
  return t('logs.archiveCategoryOperation')
}

function archiveReasonLabel(value?: string): string {
  const map: Record<string, string> = {
    manual_clear: 'logs.archiveReasonManual',
    retention: 'logs.archiveReasonRetention',
    max_rows: 'logs.archiveReasonMaxRows'
  }
  return map[value || ''] ? t(map[value || '']) : value || '-'
}

function archiveOperator(row: LogArchiveEntry): string {
  if (row.operator?.email) return row.operator.email
  if (row.operator?.user_id) return `#${row.operator.user_id}`
  return t('logs.archiveSystem')
}

function archiveRangeText(row: LogArchiveEntry): string {
  if (!row.first_created_at && !row.last_created_at) return '-'
  const first = row.first_created_at ? formatDate(row.first_created_at) : '-'
  const last = row.last_created_at ? formatDate(row.last_created_at) : '-'
  return `${first} ~ ${last}`
}

function archiveFileName(row: LogArchiveEntry): string {
  return row.file?.name?.split('/').pop() || ''
}

function formatBytes(bytes: number): string {
  if (!bytes) return '-'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

async function downloadArchive(row: LogArchiveEntry): Promise<void> {
  if (!row.archive_id || row.missing) return
  try {
    await downloadFile(`/log-archives/${encodeURIComponent(row.archive_id)}/download`, archiveFileName(row))
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function deleteArchive(row: LogArchiveEntry): Promise<void> {
  try {
    await ElMessageBox.confirm(
      t('logs.archiveDeleteConfirm', { file: archiveFileName(row) }),
      t('logs.archiveDeleteTitle'),
      {
        confirmButtonText: t('logs.archiveDelete'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
  } catch (_) {
    return
  }
  try {
    await api.delete(`/log-archives/${encodeURIComponent(row.archive_id)}`)
    ElMessage.success(t('logs.archiveDeleted'))
    if (archiveItems.value.length === 1 && archivePage.value > 1) {
      archivePage.value--
    }
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function loadArchiveStatus(): Promise<void> {
  try {
    const data = await api.get<LogArchivePage>('/log-archives', { page: 1, size: 1 })
    archiveEnabled.value = data.enabled !== false
    archiveDir.value = data.dir || ''
  } catch (_) {
    // 状态仅用于清空提示；失败时保持默认，不影响日志页面。
  }
}

async function clearAll(): Promise<void> {
  const isOperation = tab.value === 'operation'
  try {
    const hint = t(archiveEnabled.value ? 'logs.clearArchiveHint' : 'logs.clearNoArchiveHint')
    await ElMessageBox.confirm(
      `${t(isOperation ? 'logs.clearConfirm' : 'systemLogs.clearConfirm')}\n\n${hint}`,
      t(isOperation ? 'logs.clearTitle' : 'systemLogs.clearTitle'),
      {
        confirmButtonText: t(isOperation ? 'logs.clear' : 'systemLogs.clear'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
  } catch (_) {
    return
  }
  clearing.value = true
  try {
    const data = await api.delete<{ deleted: number }>(isOperation ? '/logs' : '/system-logs')
    ElMessage.success(
      t(isOperation ? 'logs.cleared' : 'systemLogs.cleared', { count: data?.deleted || 0 })
    )
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    clearing.value = false
  }
}

function setupTimer(): void {
  if (timer) clearInterval(timer)
  timer = null
  if (!autoRefresh.value) return
  const interval = tab.value === 'system' ? 15000 : 30000
  timer = setInterval(() => {
    // 页面不可见时暂停刷新，避免后台标签页持续请求。
    if (document.visibilityState === 'visible') load(true)
  }, interval)
}

watch([autoRefresh, tab], setupTimer)

onMounted(() => {
  load()
  loadArchiveStatus()
  setupTimer()
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
.logs-tabs {
  margin-top: 2px;
}

.logs-tabs :deep(.el-tabs__header) {
  margin: 0;
}

.logs-actions {
  justify-content: flex-end;
  margin-bottom: 14px;
}

.log-message {
  line-height: 1.7;
  word-break: break-word;
}

.archive-dir {
  margin: -4px 0 12px;
  font-size: 12px;
  word-break: break-all;
}

.archive-file {
  margin-top: 2px;
  font-size: 11px;
  color: #94a3b8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.system-log-fields {
  padding: 4px 16px 12px 48px;
}

.system-log-field {
  display: grid;
  grid-template-columns: 180px minmax(0, 1fr);
  gap: 12px;
  padding: 6px 0;
  border-bottom: 1px dashed #e2e8f0;
}

.system-log-field:last-child {
  border-bottom: none;
}

.system-log-key {
  color: #64748b;
  font-size: 12px;
  word-break: break-all;
}

.system-log-value {
  margin: 0;
  color: #334155;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.log-search {
  cursor: pointer;
  color: #94a3b8;
}

.log-search:hover {
  color: #2563eb;
}

/* 卡片内边距已取消，表格贴边显示，底部统计与分页单独补回留白 */
.logs-foot {
  padding: 12px 16px 16px;
}

@media (max-width: 720px) {
  .system-log-fields {
    padding-left: 16px;
  }

  .system-log-field {
    grid-template-columns: 1fr;
    gap: 2px;
  }
}
</style>
