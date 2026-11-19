<template>
  <div class="page" v-loading="loading">
    <section class="hero">
      <div class="hero-main">
        <h2>{{ greeting }}，{{ state.user?.email?.split('@')[0] || t('dashboard.admin') }}</h2>
        <p>{{ t('dashboard.heroDesc', { certs: data?.cert_total || 0, tasks: data?.task_enabled || 0 }) }}</p>
        <div class="hero-clock">
          <div class="clock-head">
            <div class="clock-label">
              <el-icon><Timer /></el-icon>{{ t('dashboard.systemTime') }}
            </div>
            <div class="clock-zone">{{ clockZoneText }}</div>
          </div>
          <div class="clock-value">{{ clockText }}</div>
        </div>
      </div>
      <div class="hero-side">
        <div class="hero-score">
          <div class="score-num">
            <template v-if="(data?.cert_total || 0) > 0 && (data?.deploy_total || 0) > 0">{{ Math.round(data?.success_rate || 0) }}<small>%</small></template>
            <template v-else>-</template>
          </div>
          <div class="score-label">{{ t('dashboard.deploySuccessRate') }}</div>
        </div>
        <div class="hero-mini">
          <div><span>{{ data?.deploy_total || 0 }}</span>{{ t('dashboard.totalDeployments') }}</div>
          <div><span>{{ data?.monitor_total || 0 }}</span>{{ t('dashboard.monitoredSites') }}</div>
        </div>
      </div>
    </section>

    <div v-if="(data?.cert_total || 0) === 0" class="first-run">
      <div class="first-run-main">
        <div class="first-run-title">{{ t('dashboard.firstRunTitle') }}</div>
        <div class="first-run-desc">{{ t('dashboard.firstRunDesc') }}</div>
      </div>
      <div class="first-run-actions">
        <el-button @click="router.push('/plugins')">{{ t('dashboard.firstRunPlugins') }}</el-button>
        <el-button type="primary" @click="router.push('/tasks')">{{ t('dashboard.firstRunTask') }}</el-button>
      </div>
    </div>

    <div class="grid grid-4 mt-20">
      <div class="stat-card">
        <div class="top">
          <div>
            <div class="label">{{ t('dashboard.certTotal') }}</div>
            <div class="value">{{ data?.cert_total || 0 }}</div>
          </div>
          <div class="stat-icon tone-blue"><el-icon><Document /></el-icon></div>
        </div>
        <div class="hint">{{ t('dashboard.certTotalHint') }}</div>
      </div>
      <div class="stat-card">
        <div class="top">
          <div>
            <div class="label">{{ t('dashboard.expiring30') }}</div>
            <div class="value" :style="{ color: (data?.cert_expiring || 0) > 0 ? '#dc2626' : undefined }">{{ data?.cert_expiring || 0 }}</div>
          </div>
          <div class="stat-icon tone-amber"><el-icon><AlarmClock /></el-icon></div>
        </div>
        <div class="hint">{{ t('dashboard.expiring30Hint') }}</div>
      </div>
      <div class="stat-card">
        <div class="top">
          <div>
            <div class="label">{{ t('dashboard.autoTasks') }}</div>
            <div class="value">{{ data?.task_enabled || 0 }}<span style="font-size: 14px; color: #94a3b8"> / {{ data?.task_total || 0 }}</span></div>
          </div>
          <div class="stat-icon tone-green"><el-icon><Promotion /></el-icon></div>
        </div>
        <div class="hint">{{ t('dashboard.autoTasksHint') }}</div>
      </div>
      <div class="stat-card">
        <div class="top">
          <div>
            <div class="label">{{ t('dashboard.monitorIssues') }}</div>
            <div class="value" :style="{ color: (data?.monitor_issue || 0) > 0 ? '#dc2626' : undefined }">{{ data?.monitor_issue || 0 }}</div>
          </div>
          <div class="stat-icon tone-red"><el-icon><Warning /></el-icon></div>
        </div>
        <div class="hint">{{ t('dashboard.monitorIssuesHint') }}</div>
      </div>
    </div>

    <div class="grid grid-2 mt-20">
      <div class="k-card">
        <div class="card-head">
          <div>
            <h3>{{ t('dashboard.expiryDistribution') }}</h3>
            <div class="sub">{{ t('dashboard.expirySub') }}</div>
          </div>
          <el-button text type="primary" @click="router.push('/certificates')">{{ t('dashboard.viewAll') }}</el-button>
        </div>
        <div v-if="(data?.cert_total || 0) === 0" class="empty-mini">{{ t('dashboard.noCerts') }}</div>
        <template v-else>
          <div v-for="(bucket, index) in data?.expiry_buckets || []" :key="bucket.label" class="expiry-row">
            <div class="name">{{ bucket.label }}</div>
            <div class="track">
              <div class="fill" :style="{ width: bucketPercent(bucket.count), background: bucketColor(index) }"></div>
            </div>
            <div class="num">{{ bucket.count }}</div>
          </div>
        </template>
        <div class="health-note">
          <el-icon><InfoFilled /></el-icon>
          {{ t('dashboard.renewalHint') }}
        </div>
      </div>

      <div class="k-card">
        <div class="card-head">
          <div>
            <h3>{{ t('dashboard.trend14') }}</h3>
            <div class="sub">{{ t('dashboard.trendSub') }}</div>
          </div>
          <span class="tag-soft tag-blue">{{ t('dashboard.certCount', { count: trendTotal }) }}</span>
        </div>
        <div class="bars" @mouseleave="hoverIndex = -1">
          <div
            v-for="(point, index) in data?.trend || []"
            :key="point.date"
            class="bar-col"
            @mouseenter="hoverIndex = index"
          >
            <div class="bar-track">
              <div
                v-if="point.count > 0"
                class="bar-fill"
                :style="{ height: trendHeight(point.count), background: trendColor(point.count) }"
              ></div>
            </div>
            <div class="bar-label">{{ index % 2 === 0 ? point.date.slice(5) : '' }}</div>
            <div v-if="hoverIndex === index" class="bar-tip">
              {{ point.date }} · {{ t('dashboard.certCount', { count: point.count }) }}
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="grid grid-2 mt-20">
      <div class="k-card">
        <div class="card-head">
          <div>
            <h3>{{ t('dashboard.recentTasks') }}</h3>
            <div class="sub">{{ t('dashboard.recentTasksSub') }}</div>
          </div>
          <el-button text type="primary" @click="router.push('/tasks')">{{ t('dashboard.taskList') }}</el-button>
        </div>
        <div v-if="!data?.recent_tasks?.length" class="empty-mini">{{ t('dashboard.noTasks') }}</div>
        <div v-for="task in data?.recent_tasks || []" :key="task.id" class="list-item" style="cursor: pointer" @click="router.push('/tasks')">
          <span class="list-dot" :style="{ background: taskDot(task.status) }"></span>
          <div class="list-main">
            <div class="list-title">{{ task.name }}</div>
            <div class="list-sub">{{ shortDomains(task.domains) }}</div>
          </div>
          <el-tag :type="taskState(task.status).type" size="small" effect="light" round>{{ taskState(task.status).label }}</el-tag>
          <span class="list-time">{{ fromNow(task.last_run_at || task.created_at) }}</span>
        </div>
      </div>

      <div class="k-card">
        <div class="card-head">
          <div>
            <h3>{{ t('dashboard.recentLogs') }}</h3>
            <div class="sub">{{ t('dashboard.recentLogsSub') }}</div>
          </div>
          <el-button text type="primary" @click="router.push('/logs')">{{ t('dashboard.allLogs') }}</el-button>
        </div>
        <div v-if="!data?.recent_logs?.length" class="empty-mini">{{ t('dashboard.noLogs') }}</div>
        <div v-for="log in data?.recent_logs || []" :key="log.id" class="list-item">
          <span class="list-dot" :style="{ background: logDot(log.level) }"></span>
          <div class="list-main">
            <div class="list-title" style="white-space: normal">{{ log.message }}</div>
            <div class="list-sub">{{ log.source }} · {{ fromNow(log.created_at) }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { api } from '../api'
import { state } from '../store'
import { fromNow, shortDomains, taskState } from '../utils'
import type { Dashboard } from '../types'

const router = useRouter()
const { t } = useI18n()
const loading = ref(false)
const data = ref<Dashboard | null>(null)
// 悬停的柱状图列索引：整张趋势图只渲染一个原生气泡，避免逐个 el-tooltip 淡出叠加成拖影。
const hoverIndex = ref(-1)
let timer: ReturnType<typeof setInterval> | null = null

// 仪表盘时钟以服务端返回的时间为基准，按客户端时钟推进，避免浏览器时区/时间偏差。
const clockNow = ref(new Date())
const clockSkew = ref(0)
const clockOffset = ref(-new Date().getTimezoneOffset() * 60)
const clockZone = ref('')
let clockTimer: ReturnType<typeof setInterval> | null = null

function pad2(value: number): string {
  return String(value).padStart(2, '0')
}

const clockText = computed(() => {
  // 用 UTC getter 读取“服务器本地时间”，显示结果不受浏览器时区影响。
  const date = new Date(clockNow.value.getTime() + clockOffset.value * 1000)
  return `${date.getUTCFullYear()}-${pad2(date.getUTCMonth() + 1)}-${pad2(date.getUTCDate())} ${pad2(date.getUTCHours())}:${pad2(date.getUTCMinutes())}:${pad2(date.getUTCSeconds())}`
})

const clockZoneText = computed(() => {
  const seconds = clockOffset.value
  const sign = seconds >= 0 ? '+' : '-'
  const abs = Math.abs(seconds)
  const zone = `UTC${sign}${pad2(Math.floor(abs / 3600))}:${pad2(Math.floor((abs % 3600) / 60))}`
  return clockZone.value ? `${zone} · ${clockZone.value}` : zone
})

function tickClock(): void {
  clockNow.value = new Date(Date.now() + clockSkew.value)
}

function syncServerClock(value: Dashboard | null): void {
  if (!value?.server_time) return
  const serverTime = new Date(value.server_time)
  if (Number.isNaN(serverTime.getTime())) return
  clockSkew.value = serverTime.getTime() - Date.now()
  if (Number.isFinite(value.server_utc_offset)) {
    clockOffset.value = value.server_utc_offset
  }
  clockZone.value = value.server_timezone || ''
  tickClock()
}

const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour < 6) return t('dashboard.greetingNight')
  if (hour < 12) return t('dashboard.greetingMorning')
  if (hour < 18) return t('dashboard.greetingAfternoon')
  return t('dashboard.greetingEvening')
})

const trendTotal = computed(() => (data.value?.trend || []).reduce((sum, item) => sum + item.count, 0))
const maxTrend = computed(() => Math.max(1, ...(data.value?.trend || []).map((item) => item.count)))
const maxBucket = computed(() => Math.max(1, ...(data.value?.expiry_buckets || []).map((item) => item.count)))

function bucketPercent(count: number): string {
  return `${Math.max(4, (count / maxBucket.value) * 100)}%`
}

function bucketColor(index: number): string {
  return ['#ef4444', '#f97316', '#f59e0b', '#3b82f6', '#10b981'][index] || '#3b82f6'
}

// 柱高按当日数量相对峰值计算，统一从轨道底部向上生长，保证所有柱子落在同一基线上。
// 0 值不渲染柱子（见模板 v-if），因此这里只处理 count > 0 的情况。
function trendHeight(count: number): string {
  return `${Math.max(8, (count / maxTrend.value) * 100)}%`
}

// 颜色深浅同样反映数量，便于一眼看出哪几天更密集。
function trendColor(count: number): string {
  // 0→1 映射到 30%→100% 的色阶，保证少量数据也看得出差异。
  const ratio = Math.min(1, count / maxTrend.value)
  const t = 0.3 + 0.7 * ratio
  const from = [147, 197, 253] // blue-300
  const to = [37, 99, 235] // blue-600
  const [r, g, b] = from.map((value, index) => Math.round(value + (to[index] - value) * t))
  return `rgb(${r}, ${g}, ${b})`
}

function taskDot(status: string): string {
  const map: Record<string, string> = { success: '#10b981', partial: '#f59e0b', failed: '#ef4444', running: '#3b82f6' }
  return map[status] || '#94a3b8'
}

function logDot(level: string): string {
  const map: Record<string, string> = { success: '#10b981', warning: '#f59e0b', error: '#ef4444' }
  return map[level] || '#3b82f6'
}

async function load(): Promise<void> {
  loading.value = true
  try {
    data.value = await api.get<Dashboard>('/dashboard')
    syncServerClock(data.value)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 60 * 1000)
  clockTimer = setInterval(tickClock, 1000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (clockTimer) clearInterval(clockTimer)
})
</script>

<style scoped>
.hero {
  border-radius: 16px;
  padding: 24px 26px;
  background: #ffffff;
  border: 1px solid var(--kp-border);
  display: flex;
  justify-content: space-between;
  gap: 30px;
}

.hero-main {
  max-width: 640px;
}

.hero h2 {
  font-size: 22px;
  margin: 0 0 8px;
  letter-spacing: -0.01em;
}

.hero p {
  margin: 0;
  color: var(--kp-muted);
  font-size: 13.5px;
  line-height: 1.75;
}

.hero-side {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 14px;
  min-width: 220px;
}

.hero-score {
  text-align: center;
  padding: 14px 18px;
  border-radius: 12px;
  background: #f8fafc;
}

.score-num {
  font-size: 32px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--kp-text);
}

.score-num small {
  font-size: 15px;
}

.score-label {
  color: var(--kp-muted);
  font-size: 11px;
  margin-top: 2px;
}

.hero-mini {
  display: flex;
  gap: 18px;
  justify-content: center;
  color: var(--kp-muted);
  font-size: 11px;
}

.hero-mini span {
  display: block;
  color: var(--kp-text);
  font-size: 15px;
  font-weight: 600;
  text-align: center;
}

.hero-clock {
  display: inline-block;
  margin-top: 14px;
  text-align: left;
  border-radius: 12px;
}

.clock-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  width: 100%;
}

.clock-label {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 4px;
  color: var(--kp-muted);
  font-size: 10px;
}

.clock-value {
  margin-top: 3px;
  color: var(--kp-text);
  font-size: 17px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.02em;
  white-space: nowrap;
}

.clock-zone {
  color: var(--kp-muted);
  font-size: 10px;
  white-space: nowrap;
}

.health-note {
  margin-top: 18px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #f8fafc;
  color: #64748b;
  font-size: 12px;
  display: flex;
  align-items: center;
  gap: 7px;
}

.first-run {
  margin-top: 20px;
  padding: 18px 22px;
  border: 1px solid #e0e7ff;
  border-radius: 14px;
  background: linear-gradient(135deg, #eef2ff 0%, #f8fafc 100%);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
}

.first-run-title {
  color: #312e81;
  font-size: 15px;
  font-weight: 600;
}

.first-run-desc {
  margin-top: 6px;
  color: #475569;
  font-size: 13px;
  line-height: 1.7;
}

.first-run-actions {
  display: flex;
  gap: 10px;
  flex-shrink: 0;
}

@media (max-width: 760px) {
  .first-run {
    flex-direction: column;
    align-items: flex-start;
  }
}

.empty-mini {
  color: #94a3b8;
  font-size: 13px;
  padding: 26px 0;
  text-align: center;
}

@media (max-width: 860px) {
  .hero {
    flex-direction: column;
    padding: 24px;
  }
  .hero-side {
    flex-direction: row;
    flex-wrap: wrap;
    min-width: 0;
  }
  .hero-score {
    flex: 1;
  }
  .hero-mini {
    flex: 1;
    align-items: center;
  }
}
</style>
