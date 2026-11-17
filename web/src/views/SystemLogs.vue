<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('systemLogs.title') }}</h1>
        <p>{{ t('systemLogs.subtitle', { days: retentionDays }) }}</p>
      </div>
      <div class="page-actions">
        <el-select v-model="level" :placeholder="t('systemLogs.allLevels')" style="width: 130px" @change="reload">
          <el-option v-for="item in levels" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
        <el-select v-model="component" clearable :placeholder="t('systemLogs.allComponents')" style="width: 150px" @change="reload">
          <el-option v-for="item in components" :key="item" :label="componentLabel(item)" :value="item" />
        </el-select>
        <el-date-picker
          v-model="range"
          type="daterange"
          unlink-panels
          range-separator="-"
          style="width: 240px"
          @change="reload"
        />
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
        <el-dropdown trigger="click" @command="exportLogs">
          <el-button :icon="Download" :loading="exporting" :title="t('systemLogs.exportHint')">
            {{ exporting ? t('systemLogs.exporting') : t('systemLogs.export') }}
            <el-icon class="el-icon--right"><ArrowDown /></el-icon>
          </el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="csv">{{ t('systemLogs.exportCsv') }}</el-dropdown-item>
              <el-dropdown-item command="json">{{ t('systemLogs.exportJson') }}</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-button :icon="Refresh" circle @click="load()" />
        <el-button type="danger" plain :icon="Delete" :loading="clearing" @click="clearAll">
          {{ t('systemLogs.clear') }}
        </el-button>
      </div>
    </div>

    <el-alert
      v-if="!enabled"
      :title="t('systemLogs.disabled')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
    />

    <div class="k-card" style="padding: 0; overflow: hidden">
      <el-table :data="items" v-loading="loading" row-key="id" style="width: 100%">
        <el-table-column type="expand">
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
        <el-table-column width="160" :label="t('systemLogs.time')">
          <template #default="{ row }">
            <span class="muted">{{ formatDate(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column width="96" :label="t('systemLogs.level')">
          <template #default="{ row }">
            <span class="tag-soft" :class="levelClass(row.level)">{{ t(levelKey(row.level)) }}</span>
          </template>
        </el-table-column>
        <el-table-column width="130" :label="t('systemLogs.component')">
          <template #default="{ row }">
            <span class="muted">{{ componentLabel(row.component) }}</span>
          </template>
        </el-table-column>
        <el-table-column min-width="420" :label="t('systemLogs.message')">
          <template #default="{ row }">
            <span class="system-log-message">{{ row.message }}</span>
          </template>
        </el-table-column>
        <template #empty>
          <div style="padding: 40px 0; color: #94a3b8">{{ t('systemLogs.empty') }}</div>
        </template>
      </el-table>
      <div class="flex-between logs-foot">
        <span class="muted">{{ t('systemLogs.total', { total }) }}</span>
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
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, Delete, Download, Refresh, Search } from '@element-plus/icons-vue'
import { api, downloadFile } from '../api'
import { formatDate } from '../utils'
import type { SystemLogEntry, SystemLogPage } from '../types'

const { t } = useI18n()
const loading = ref(false)
const clearing = ref(false)
const exporting = ref(false)
const items = ref<SystemLogEntry[]>([])
const total = ref(0)
const page = ref(1)
const size = 30
const level = ref('')
const component = ref('')
const query = ref('')
const range = ref<[Date, Date] | null>(null)
const components = ref<string[]>([])
const retentionDays = ref(7)
const enabled = ref(true)
const autoRefresh = ref(true)
let timer: ReturnType<typeof setInterval> | null = null

const levels = [
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

function componentLabel(value: string): string {
  const key = componentKeys[value]
  return key ? t(key) : value
}

function levelKey(value: string): string {
  const map: Record<string, string> = {
    debug: 'systemLogs.levelDebug',
    info: 'systemLogs.levelInfo',
    warn: 'systemLogs.levelWarn',
    warning: 'systemLogs.levelWarn',
    error: 'systemLogs.levelError'
  }
  return map[value] || 'systemLogs.levelInfo'
}

function levelClass(value: string): string {
  const map: Record<string, string> = {
    debug: 'tag-gray',
    info: 'tag-blue',
    warn: 'tag-amber',
    warning: 'tag-amber',
    error: 'tag-red'
  }
  return map[value] || 'tag-gray'
}

// 列表筛选条件与后端解析保持一致。
function filterParams(): Record<string, string> {
  const params: Record<string, string> = {}
  if (level.value) params.level = level.value
  if (component.value) params.component = component.value
  if (query.value.trim()) params.q = query.value.trim()
  if (range.value && range.value.length === 2) {
    const [start, end] = range.value
    params.from = start.toISOString()
    // to 为次日零点，后端使用左闭右开区间。
    const next = new Date(end.getFullYear(), end.getMonth(), end.getDate() + 1)
    params.to = next.toISOString()
  }
  return params
}

async function load(silent = false): Promise<void> {
  if (!silent) loading.value = true
  try {
    const data = await api.get<SystemLogPage>('/system-logs', { page: page.value, size, ...filterParams() })
    items.value = data.items || []
    total.value = data.total || 0
    components.value = data.components || []
    retentionDays.value = data.retention_days || 7
    enabled.value = data.enabled !== false
  } catch (error: any) {
    if (!silent) ElMessage.error(error.message)
  } finally {
    if (!silent) loading.value = false
  }
}

function reload(): void {
  page.value = 1
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
    ElMessage.warning(t('systemLogs.exportEmpty'))
    return
  }
  exporting.value = true
  try {
    const params = new URLSearchParams(filterParams())
    params.set('format', format)
    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (timezone) params.set('tz', timezone)
    await downloadFile(
      `/system-logs/export?${params.toString()}`,
      `lactssl-system-logs-${fileStamp()}.${format}`
    )
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    exporting.value = false
  }
}

function fieldEntries(row: SystemLogEntry): Array<[string, unknown]> {
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
  if (!autoRefresh.value) return
  timer = setInterval(() => {
    // 页面不可见时暂停刷新，避免后台标签页持续请求。
    if (document.visibilityState === 'visible') load(true)
  }, 15000)
}

watch(autoRefresh, setupTimer)

onMounted(() => {
  load()
  setupTimer()
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
.system-log-search {
  cursor: pointer;
  color: #94a3b8;
}

.system-log-search:hover {
  color: #2563eb;
}

.system-log-message {
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
