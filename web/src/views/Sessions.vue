<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('sessions.title') }}</h1>
        <p>{{ t('sessions.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle :loading="loading" @click="load" />
        <el-button type="danger" plain :disabled="!hasOther" :loading="revokingOthers" @click="revokeOthers">
          {{ t('sessions.revokeOthers') }}
        </el-button>
      </div>
    </div>

    <div class="k-card" style="padding: 8px 16px 16px">
      <el-table :data="items" v-loading="loading" style="width: 100%">
        <el-table-column min-width="260" :label="t('sessions.device')">
          <template #default="{ row }">
            <div class="flex gap-10" style="align-items: center">
              <el-icon size="18" color="#64748b"><Monitor /></el-icon>
              <div style="min-width: 0">
                <div style="display: flex; align-items: center; gap: 8px; font-weight: 600">
                  <span>{{ deviceLabel(row.user_agent) }}</span>
                  <span v-if="row.current" class="tag-soft tag-green">{{ t('sessions.current') }}</span>
                </div>
                <div class="muted" style="margin-top: 2px; font-size: 12px; word-break: break-all">
                  {{ row.user_agent || t('sessions.unknownDevice') }}
                </div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column width="150" :label="t('sessions.ip')">
          <template #default="{ row }">
            <span class="mono">{{ row.ip || t('common.none') }}</span>
          </template>
        </el-table-column>
        <el-table-column width="170" :label="t('sessions.loginAt')">
          <template #default="{ row }">
            <span class="muted">{{ formatDate(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column width="150" :label="t('sessions.lastSeen')">
          <template #default="{ row }">{{ fromNow(row.last_seen_at) }}</template>
        </el-table-column>
        <el-table-column width="170" :label="t('sessions.expiresAt')">
          <template #default="{ row }">
            <span class="muted">{{ formatDate(row.expires_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column width="90" align="right" :label="t('common.actions')">
          <template #default="{ row }">
            <el-button v-if="!row.current" text type="danger" size="small" @click="revoke(row)">
              {{ t('sessions.revoke') }}
            </el-button>
            <span v-else class="muted">{{ t('common.none') }}</span>
          </template>
        </el-table-column>
        <template #empty>
          <div style="padding: 40px 0; color: #94a3b8">{{ t('sessions.empty') }}</div>
        </template>
      </el-table>
      <div class="flex-between" style="margin-top: 16px">
        <span class="muted">{{ t('sessions.total', { count: items.length }) }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { formatDate, fromNow } from '../utils'
import type { Session } from '../types'

const { t } = useI18n()
const loading = ref(false)
const revokingOthers = ref(false)
const items = ref<Session[]>([])

const hasOther = computed(() => items.value.some((item) => !item.current))

async function load(): Promise<void> {
  loading.value = true
  try {
    items.value = await api.get<Session[]>('/sessions')
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

async function revoke(session: Session): Promise<void> {
  try {
    await ElMessageBox.confirm(t('sessions.revokeConfirm'), t('sessions.revokeTitle'), {
      confirmButtonText: t('sessions.revoke'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
    await api.delete(`/sessions/${encodeURIComponent(session.id)}`)
    ElMessage.success(t('sessions.revoked'))
    await load()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.message)
  }
}

async function revokeOthers(): Promise<void> {
  try {
    await ElMessageBox.confirm(t('sessions.revokeOthersConfirm'), t('sessions.revokeOthersTitle'), {
      confirmButtonText: t('sessions.revokeOthers'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
    revokingOthers.value = true
    const data = await api.post<{ revoked: number }>('/sessions/revoke-others')
    ElMessage.success(t('sessions.revokeOthersDone', { count: data.revoked || 0 }))
    await load()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.message)
  } finally {
    revokingOthers.value = false
  }
}

// 从 User-Agent 中提取可读的浏览器与系统名称，原始内容仍会显示在下方。
function deviceLabel(userAgent: string): string {
  if (!userAgent) return t('sessions.unknownDevice')
  const browser =
    /Edg\//.test(userAgent) ? 'Edge'
      : /OPR\/|Opera/.test(userAgent) ? 'Opera'
        : /Firefox\//.test(userAgent) ? 'Firefox'
          : /Chrome\//.test(userAgent) ? 'Chrome'
            : /Version\/.*Safari\//.test(userAgent) ? 'Safari'
              : ''
  const os =
    /Windows NT/.test(userAgent) ? 'Windows'
      : /Android/.test(userAgent) ? 'Android'
        : /iPhone|iPad|iPod/.test(userAgent) ? 'iOS'
          : /Mac OS X/.test(userAgent) ? 'macOS'
            : /Linux/.test(userAgent) ? 'Linux'
              : ''
  if (browser && os) return `${browser} · ${os}`
  return browser || os || t('sessions.unknownDevice')
}

onMounted(load)
</script>
