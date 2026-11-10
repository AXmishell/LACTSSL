<template>
  <div class="page" v-loading="loading">
    <section class="hero">
      <div class="hero-main">
        <h2>{{ greeting }}，{{ state.user?.email?.split('@')[0] || t('dashboard.admin') }}</h2>
        <p>{{ t('dashboard.heroDesc', { certs: data?.cert_total || 0, tasks: data?.task_enabled || 0 }) }}</p>
        <div class="hero-actions">
          <el-button type="primary" size="large" @click="router.push('/tasks')">
            <el-icon><Plus /></el-icon>{{ t('dashboard.newTask') }}
          </el-button>
          <el-button size="large" @click="router.push('/monitors')">
            <el-icon><Monitor /></el-icon>{{ t('dashboard.addMonitor') }}
          </el-button>
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
        <div class="bars">
          <div v-for="(point, index) in data?.trend || []" :key="point.date" class="bar-col">
            <el-tooltip :content="`${point.date} · ${t('dashboard.certCount', { count: point.count })}`" placement="top">
              <div class="bar-track">
                <div class="bar-fill" :style="{ height: trendHeight(point.count) }"></div>
              </div>
            </el-tooltip>
            <div class="bar-label">{{ index % 2 === 0 ? point.date.slice(5) : '' }}</div>
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
let timer: ReturnType<typeof setInterval> | null = null

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

function trendHeight(count: number): string {
  if (!count) return '2px'
  return `${Math.max(8, (count / maxTrend.value) * 100)}%`
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
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 60 * 1000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
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

.hero-actions {
  display: flex;
  gap: 10px;
  margin-top: 18px;
}

.hero-side {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 14px;
  min-width: 180px;
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
