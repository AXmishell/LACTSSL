<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('logs.title') }}</h1>
        <p>{{ subtitle }}</p>
      </div>
      <el-tabs v-model="tab" class="logs-tabs" @tab-change="onTabChange">
        <el-tab-pane :label="t('logs.tabOperation')" name="operation" />
        <el-tab-pane :label="t('logs.tabSystem')" name="system" />
      </el-tabs>
    </div>

    <div class="page-actions logs-actions">
      <template v-if="tab === 'operation'">
        <el-select v-model="source" :placeholder="t('logs.allSources')" style="width: 140px" @change="reload">
          <el-option v-for="item in sources" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
        <el-select v-model="level" :placeholder="t('logs.allLevels')" style="width: 120px" @change="reload">
          <el-option v-for="item in levels" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
      </template>
      <template v-else>
        <el-select v-model="systemLevel" :placeholder="t('systemLogs.allLevels')" style="width: 130px" @change="reload">
          <el-option v-for="item in systemLevels" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
        <el-select v-model="component" clearable :placeholder="t('systemLogs.allComponents')" style="width: 150px" @change="reload">
          <el-option v-for="item in components" :key="item" :label="componentLabel(item)" :value="item" />
        </el-select>
        <el-input
          v-model="query"
          clearable
          maxlength="100"
          :placeholder="t('systemLogs.searchPlaceholder')"
          style="width: 210px"
          @keyup.enter="reload"
          @clear="reload"
        >
          <template #suffix>
            <el-icon class="system-log-search" @click="reload"><Search /></el-icon>
          </template>
        </el-input>
        <el-switch v-model="autoRefresh" :active-text="t('systemLogs.autoRefresh')" />
      </template>
      <el-date-picker
        v-model="activeRange"
        type="daterange"
        unlink-panels
        range-separator="-"
        style="width: 240px"
        @change="reload"
      />
      <el-dropdown trigger="click" @command="exportLogs">
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
        v-if="tab === 'system'"
        type="danger"
        plain
        :icon="Delete"
        :loading="clearing"
        @click="clearAll"
      >
        {{ t('systemLogs.clear') }}
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

    <div class="k-card" style="padding: 0; overflow: hidden">
      <div class="table-scroll">
        <el-table :data="items" v-loading="loading" row-key="id" style="width: 100%">
          <el-table-column v-if="tab === 'system'" type="expand">
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
          <el-table-column v-else width="130" :label="t('systemLogs.component')">
            <template #default="{ row }">
              <span class="muted">{{ componentLabel(row.component) }}</span>
            </template>
          </el-table-column>
          <el-table-column
            min-width="380"
            :label="tab === 'operation' ? t('logs.content') : t('systemLogs.message')"
          >
            <template #default="{ row }">
              <span class="log-message">{{ row.message }}</span>
            </template>
          </el-table-column>
          <el-table-column width="170" :label="t('logs.time')">
            <template #default="{ row }">
              <span class="muted">{{ formatDate(row.created_at) }}</span>
            </template>
          </el-table-column>
          <template #empty>
            <div style="padding: 40px 0; color: #94a3b8">
              {{ tab === 'operation' ? t('logs.empty') : t('systemLogs.empty') }}
            </div>
          </template>
        </el-table>
      </div>
      <div class="flex-between logs-foot">
        <span class="muted">{{ t('logs.total', { total }) }}</span>
        <el-pagination
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
import type { LogPage, SystemLogPage } from '../types'

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
const tab = ref(route.meta.logTab === 'system' ? 'system' : 'operation')
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
const operationRange = ref<[Date, Date] | null>(null)

// 系统日志筛选
const systemLevel = ref('')
const component = ref('')
const query = ref('')
const systemRange = ref<[Date, Date] | null>(null)
const components = ref<string[]>([])
const retentionDays = ref(7)
const enabled = ref(true)
const autoRefresh = ref(true)
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

const subtitle = computed(() =>
  tab.value === 'operation'
    ? t('logs.subtitle')
    : t('systemLogs.subtitle', { days: retentionDays.value })
)

const activeRange = computed({
  get: () => (tab.value === 'operation' ? operationRange.value : systemRange.value),
  set: (value: [Date, Date] | null) => {
    if (tab.value === 'operation') {
      operationRange.value = value
    } else {
      systemRange.value = value
    }
  }
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
  if (tab.value === 'operation') {
    if (source.value) params.source = source.value
    if (level.value) params.level = level.value
  } else {
    if (systemLevel.value) params.level = systemLevel.value
    if (component.value) params.component = component.value
    if (query.value.trim()) params.q = query.value.trim()
  }
  return params
}

async function load(silent = false): Promise<void> {
  const seq = ++loadSeq
  if (!silent) loading.value = true
  try {
    if (tab.value === 'operation') {
      const data = await api.get<LogPage>('/logs', { page: page.value, size, ...filterParams() })
      if (seq !== loadSeq) return
      items.value = data.items || []
      total.value = data.total || 0
    } else {
      const data = await api.get<SystemLogPage>('/system-logs', {
        page: page.value,
        size,
        ...filterParams()
      })
      if (seq !== loadSeq) return
      items.value = data.items || []
      total.value = data.total || 0
      components.value = data.components || []
      retentionDays.value = data.retention_days || 7
      enabled.value = data.enabled !== false
    }
  } catch (error: any) {
    if (seq === loadSeq && !silent) ElMessage.error(error.message)
  } finally {
    if (seq === loadSeq && !silent) loading.value = false
  }
}

function reload(): void {
  page.value = 1
  load()
}

function onTabChange(): void {
  page.value = 1
  items.value = []
  total.value = 0
  load()
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
      tab.value === 'operation' ? t('logs.exportEmpty') : t('systemLogs.exportEmpty')
    )
    return
  }
  exporting.value = true
  try {
    const params = new URLSearchParams(filterParams())
    params.set('format', format)
    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (timezone) params.set('tz', timezone)
    const endpoint = tab.value === 'operation' ? '/logs/export' : '/system-logs/export'
    const prefix = tab.value === 'operation' ? 'lactssl-logs' : 'lactssl-system-logs'
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

async function clearAll(): Promise<void> {
  try {
    await ElMessageBox.confirm(t('systemLogs.clearConfirm'), t('systemLogs.clearTitle'), {
      confirmButtonText: t('systemLogs.clear'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  clearing.value = true
  try {
    const data = await api.delete<{ deleted: number }>('/system-logs')
    ElMessage.success(t('systemLogs.cleared', { count: data?.deleted || 0 }))
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
  if (tab.value === 'system' && !autoRefresh.value) return
  const interval = tab.value === 'system' ? 15000 : 30000
  timer = setInterval(() => {
    // 页面不可见时暂停刷新，避免后台标签页持续请求。
    if (document.visibilityState === 'visible') load(true)
  }, interval)
}

watch([autoRefresh, tab], setupTimer)

onMounted(() => {
  load()
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

.system-log-search {
  cursor: pointer;
  color: #94a3b8;
}

.system-log-search:hover {
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
