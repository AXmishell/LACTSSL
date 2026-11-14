<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('logs.title') }}</h1>
        <p>{{ t('logs.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-select v-model="source" :placeholder="t('logs.allSources')" style="width: 140px" @change="reload">
          <el-option v-for="item in sources" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
        <el-select v-model="level" :placeholder="t('logs.allLevels')" style="width: 120px" @change="reload">
          <el-option v-for="item in levels" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
        <el-date-picker
          v-model="range"
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
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <div class="k-card" style="padding: 0; overflow: hidden">
      <el-table :data="items" v-loading="loading" style="width: 100%">
        <el-table-column width="110" :label="t('logs.level')">
          <template #default="{ row }">
            <span class="tag-soft" :class="levelClass(row.level)">{{ t(levelKey(row.level)) }}</span>
          </template>
        </el-table-column>
        <el-table-column width="130" :label="t('logs.source')">
          <template #default="{ row }">
            <span class="muted">{{ sourceLabel(row.source) }}</span>
          </template>
        </el-table-column>
        <el-table-column min-width="360" :label="t('logs.content')">
          <template #default="{ row }">
            <span style="line-height: 1.7; word-break: break-word">{{ row.message }}</span>
          </template>
        </el-table-column>
        <el-table-column width="170" :label="t('logs.time')">
          <template #default="{ row }">
            <span class="muted">{{ formatDate(row.created_at) }}</span>
          </template>
        </el-table-column>
        <template #empty>
          <div style="padding: 40px 0; color: #94a3b8">{{ t('logs.empty') }}</div>
        </template>
      </el-table>
      <div class="flex-between logs-foot">
        <span class="muted">{{ t('logs.total', { total }) }}</span>
        <el-pagination
          v-model:current-page="page"
          :page-size="size"
          :total="total"
          layout="prev, pager, next"
          background
          @current-change="load"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { ArrowDown, Download, Refresh } from '@element-plus/icons-vue'
import { api, downloadFile } from '../api'
import { formatDate } from '../utils'
import type { LogEntry, LogPage } from '../types'

const { t } = useI18n()
const loading = ref(false)
const exporting = ref(false)
const items = ref<LogEntry[]>([])
const total = ref(0)
const page = ref(1)
const size = 30
const source = ref('')
const level = ref('')
const range = ref<[Date, Date] | null>(null)
let timer: ReturnType<typeof setInterval> | null = null

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
  { value: 'cert', label: 'logs.sourceCert' }
]

const levels = [
  { value: '', label: 'logs.allLevels' },
  { value: 'info', label: 'logs.info' },
  { value: 'success', label: 'logs.success' },
  { value: 'warning', label: 'logs.warning' },
  { value: 'error', label: 'logs.error' }
]

function sourceLabel(value: string): string {
  const item = sources.find((entry) => entry.value === value)
  return item ? t(item.label) : value
}

function levelKey(level: string): string {
  const map: Record<string, string> = {
    info: 'logs.info',
    success: 'logs.success',
    warning: 'logs.warning',
    error: 'logs.error'
  }
  return map[level] || 'logs.info'
}

function levelClass(level: string): string {
  const map: Record<string, string> = {
    info: 'tag-blue',
    success: 'tag-green',
    warning: 'tag-amber',
    error: 'tag-red'
  }
  return map[level] || 'tag-gray'
}

// 列表与导出共用同一组筛选条件，避免“看到的”和“导出的”不一致。
function filterParams(): Record<string, string> {
  const params: Record<string, string> = {}
  if (source.value) params.source = source.value
  if (level.value) params.level = level.value
  if (range.value && range.value.length === 2) {
    const [start, end] = range.value
    params.from = start.toISOString()
    // 结束日期按本地整天处理，to 为次日零点，后端使用左闭右开区间。
    const next = new Date(end.getFullYear(), end.getMonth(), end.getDate() + 1)
    params.to = next.toISOString()
  }
  return params
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const data = await api.get<LogPage>('/logs', { page: page.value, size, ...filterParams() })
    items.value = data.items || []
    total.value = data.total || 0
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
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
    ElMessage.warning(t('logs.exportEmpty'))
    return
  }
  exporting.value = true
  try {
    const params = new URLSearchParams(filterParams())
    params.set('format', format)
    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (timezone) params.set('tz', timezone)
    await downloadFile(`/logs/export?${params.toString()}`, `lactssl-logs-${fileStamp()}.${format}`)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    exporting.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 30 * 1000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
/* 卡片内边距已取消，表格贴边显示，底部统计与分页单独补回留白 */
.logs-foot {
  padding: 12px 16px 16px;
}
</style>
