<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('logs.title') }}</h1>
        <p>{{ t('logs.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-select v-model="source" :placeholder="t('logs.allSources')" style="width: 150px" @change="reload">
          <el-option v-for="item in sources" :key="item.value" :label="t(item.label)" :value="item.value" />
        </el-select>
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <div class="k-card" style="padding: 8px 16px 16px">
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
      <div class="flex-between" style="margin-top: 16px">
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
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { formatDate } from '../utils'
import type { LogEntry, LogPage } from '../types'

const { t } = useI18n()
const loading = ref(false)
const items = ref<LogEntry[]>([])
const total = ref(0)
const page = ref(1)
const size = 30
const source = ref('')
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
  { value: 'system', label: 'logs.sourceSystem' }
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

async function load(): Promise<void> {
  loading.value = true
  try {
    const data = await api.get<LogPage>('/logs', { page: page.value, size, source: source.value })
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

onMounted(() => {
  load()
  timer = setInterval(load, 30 * 1000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>
