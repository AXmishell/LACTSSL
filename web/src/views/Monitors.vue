<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('monitors.title') }}</h1>
        <p>{{ t('monitors.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle @click="load" />
        <el-button type="primary" :icon="Plus" @click="openCreate">{{ t('monitors.add') }}</el-button>
      </div>
    </div>

    <div class="monitor-summary">
      <div class="summary-item">
        <span class="summary-num">{{ monitors.length }}</span>
        <span class="muted">{{ t('monitors.total') }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-num" style="color: #10b981">{{ summary.valid }}</span>
        <span class="muted">{{ t('monitors.valid') }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-num" style="color: #f59e0b">{{ summary.expiring }}</span>
        <span class="muted">{{ t('monitors.expiring') }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-num" style="color: #ef4444">{{ summary.problem }}</span>
        <span class="muted">{{ t('monitors.problem') }}</span>
      </div>
    </div>

    <div v-if="!monitors.length && !loading" class="k-card" style="text-align: center; padding: 54px 20px">
      <el-icon size="34" color="#94a3b8"><Monitor /></el-icon>
      <h3 style="margin: 14px 0 6px">{{ t('monitors.emptyTitle') }}</h3>
      <p class="muted" style="margin-top: 0">{{ t('monitors.emptyDesc') }}</p>
      <el-button type="primary" style="margin-top: 12px" @click="openCreate">{{ t('monitors.addFirst') }}</el-button>
    </div>

    <div v-else class="k-card monitor-list" v-loading="loading">
      <div class="monitor-list-head">
        <span class="monitor-head-name">
          <el-checkbox
            :model-value="allSelected"
            :indeterminate="someSelected"
            :disabled="!monitors.length"
            @change="toggleAll"
          />
          <span>{{ t('monitors.name') }}</span>
        </span>
        <span>{{ t('common.status') }}</span>
        <span>{{ t('monitors.expiresAt') }}</span>
        <span>{{ t('monitors.issuer') }}</span>
        <span>{{ t('monitors.nextCheck') }}</span>
        <span class="monitor-head-actions">{{ t('common.actions') }}</span>
      </div>
      <div v-for="monitor in monitors" :key="monitor.id" class="monitor-row" :class="{ 'is-selected': isSelected(monitor) }">
        <div class="monitor-cell monitor-name">
          <el-checkbox
            class="monitor-check"
            :model-value="isSelected(monitor)"
            @change="(value: string | number | boolean) => toggleItem(monitor, Boolean(value))"
          />
          <div class="monitor-icon" :class="`dot-${monitorState(monitor.status).type}`">
            <el-icon><Monitor /></el-icon>
          </div>
          <div class="monitor-name-text">
            <div class="cred-title">{{ monitor.name }}</div>
            <div class="mono cred-meta">{{ monitor.target }}</div>
          </div>
        </div>

        <div class="monitor-cell monitor-status">
          <el-tag :type="monitorState(monitor.status).type" effect="light" round size="small">
            {{ monitorState(monitor.status).label }}
          </el-tag>
        </div>

        <div class="monitor-cell monitor-expires">
          <div class="monitor-strong">{{ monitor.expires_at ? formatDate(monitor.expires_at, false) : t('common.none') }}</div>
          <div class="monitor-sub" :style="{ color: remainingColor(monitor.expires_at) }">
            {{ monitor.expires_at ? t('monitors.days', { days: daysLeft(monitor.expires_at) }) : t('common.none') }}
          </div>
        </div>

        <div class="monitor-cell monitor-issuer">
          <el-tooltip :content="monitor.issuer || t('common.none')" placement="top">
            <span class="monitor-ellipsis">{{ monitor.issuer || t('common.none') }}</span>
          </el-tooltip>
        </div>

        <div class="monitor-cell monitor-next">
          <div class="monitor-strong">{{ monitor.next_check_at ? formatDate(monitor.next_check_at) : t('common.none') }}</div>
          <div class="monitor-sub">{{ t('monitors.everyHours', { hours: monitor.interval_hours }) }}</div>
        </div>

        <div class="monitor-cell monitor-actions">
          <el-button text type="primary" size="small" :loading="checking === monitor.id" @click="check(monitor)">{{ t('monitors.checkNow') }}</el-button>
          <el-button text size="small" @click="openEdit(monitor)">{{ t('monitors.edit') }}</el-button>
          <el-button text type="danger" size="small" @click="remove(monitor)">{{ t('monitors.delete') }}</el-button>
        </div>
      </div>
    </div>

    <div v-if="selectedIds.length" class="batch-bar">
      <div class="batch-actions">
        <el-button size="small" type="danger" plain :icon="Delete" :loading="batchDeleting" @click="batchDelete">
          {{ t('monitors.batchDelete') }}
        </el-button>
        <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('monitors.clearSelection') }}</el-button>
      </div>
      <span class="batch-count">{{ t('monitors.selectedCount', { count: selectedIds.length }) }}</span>
    </div>

    <el-dialog v-model="dialogVisible" :title="editingId ? t('monitors.editTitle') : t('monitors.addTitle')" width="520px" destroy-on-close>
      <el-form :model="form" label-position="top">
        <el-form-item :label="t('monitors.name')">
          <el-input v-model="form.name" :placeholder="t('monitors.namePlaceholder')" maxlength="60" />
        </el-form-item>
        <el-form-item :label="t('monitors.target')" required>
          <el-input v-model="form.target" :placeholder="t('monitors.targetPlaceholder')" />
          <div class="form-help">{{ t('monitors.targetHelp') }}</div>
        </el-form-item>
        <div class="grid grid-2" style="gap: 0 16px">
          <el-form-item :label="t('monitors.interval')">
            <el-input-number v-model="form.interval_hours" :min="1" :max="720" style="width: 100%" />
            <div class="form-help">{{ t('monitors.hoursUnit') }}</div>
          </el-form-item>
          <el-form-item :label="t('monitors.enabled')">
            <div class="flex gap-10" style="height: 32px">
              <el-switch v-model="form.enabled" />
              <span class="muted">{{ form.enabled ? t('monitors.autoCheck') : t('monitors.paused') }}</span>
            </div>
          </el-form-item>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('monitors.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ t('monitors.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Close, Delete, Plus, Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { requestSecondaryVerification, secondaryVerifyHeader, withSecondaryVerify } from '../secondaryVerify'
import { daysLeft, formatDate, monitorState } from '../utils'
import type { Monitor } from '../types'

interface MonitorForm {
  name: string
  target: string
  interval_hours: number
  enabled: boolean
}

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const checking = ref(0)
const monitors = ref<Monitor[]>([])
const dialogVisible = ref(false)
const editingId = ref(0)
const selectedIds = ref<number[]>([])
const batchDeleting = ref(false)

const form = ref<MonitorForm>({ name: '', target: '', interval_hours: 24, enabled: true })

const allSelected = computed(() => monitors.value.length > 0 && selectedIds.value.length === monitors.value.length)
const someSelected = computed(() => selectedIds.value.length > 0 && !allSelected.value)

const summary = computed(() => {
  let valid = 0, expiring = 0, problem = 0
  for (const item of monitors.value) {
    if (item.status === 'valid') valid++
    else if (item.status === 'expiring') expiring++
    else if (item.status === 'error' || item.status === 'expired') problem++
  }
  return { valid, expiring, problem }
})

function remainingColor(value?: string | null): string {
  if (!value) return '#0f172a'
  const left = daysLeft(value)
  if (left < 0) return '#ef4444'
  if (left <= 30) return '#f59e0b'
  return '#10b981'
}

async function load(): Promise<void> {
  loading.value = true
  try {
    monitors.value = (await api.get<Monitor[]>('/monitors')) || []
    selectedIds.value = []
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editingId.value = 0
  Object.assign(form.value, { name: '', target: '', interval_hours: 24, enabled: true })
  dialogVisible.value = true
}

function openEdit(monitor: Monitor): void {
  editingId.value = monitor.id
  Object.assign(form.value, {
    name: monitor.name,
    target: monitor.target,
    interval_hours: monitor.interval_hours,
    enabled: monitor.enabled
  })
  dialogVisible.value = true
}

async function save(): Promise<void> {
  if (!form.value.target.trim()) {
    ElMessage.warning(t('monitors.targetRequired'))
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.value.name.trim(),
      target: form.value.target.trim(),
      interval_hours: form.value.interval_hours,
      enabled: form.value.enabled
    }
    const saved = await withSecondaryVerify('monitor_write', (headers) =>
      editingId.value ? api.put(`/monitors/${editingId.value}`, payload, headers) : api.post('/monitors', payload, headers)
    )
    if (saved === null) return
    ElMessage.success(editingId.value ? t('monitors.updated') : t('monitors.added'))
    dialogVisible.value = false
    await load()
    setTimeout(load, 2500)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function check(monitor: Monitor): Promise<void> {
  checking.value = monitor.id
  try {
    await api.post(`/monitors/${monitor.id}/check`)
    ElMessage.success(t('monitors.checking'))
    setTimeout(async () => {
      await load()
      checking.value = 0
    }, 2200)
  } catch (error: any) {
    ElMessage.error(error.message)
    checking.value = 0
  }
}

async function remove(monitor: Monitor): Promise<void> {
  try {
    await ElMessageBox.confirm(t('monitors.deleteConfirm', { name: monitor.name }), t('monitors.deleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    const deleted = await withSecondaryVerify('monitor_write', (headers) => api.delete(`/monitors/${monitor.id}`, headers))
    if (deleted === null) return
    ElMessage.success(t('monitors.deleted'))
    load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function isSelected(monitor: Monitor): boolean {
  return selectedIds.value.includes(monitor.id)
}

function toggleItem(monitor: Monitor, checked: boolean): void {
  if (checked) {
    if (!selectedIds.value.includes(monitor.id)) selectedIds.value.push(monitor.id)
  } else {
    selectedIds.value = selectedIds.value.filter((id) => id !== monitor.id)
  }
}

function toggleAll(value: string | number | boolean): void {
  selectedIds.value = value ? monitors.value.map((monitor) => monitor.id) : []
}

function clearSelection(): void {
  selectedIds.value = []
}

async function batchDelete(): Promise<void> {
  const ids = [...selectedIds.value]
  if (!ids.length) return
  try {
    await ElMessageBox.confirm(t('monitors.batchDeleteConfirm', { count: ids.length }), t('monitors.batchDeleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  const token = await requestSecondaryVerification('monitor_write')
  if (token === null) return
  const verifyHeaders: Record<string, string> = token ? { [secondaryVerifyHeader]: token } : {}
  batchDeleting.value = true
  let failed = 0
  try {
    for (const id of ids) {
      try {
        await api.delete(`/monitors/${id}`, verifyHeaders)
      } catch (error: any) {
        failed++
        const name = monitors.value.find((monitor) => monitor.id === id)?.name || id
        ElMessage.error(`${name}: ${error.message}`)
      }
    }
  } finally {
    batchDeleting.value = false
  }
  if (failed) ElMessage.warning(t('monitors.batchDeleteFailed', { count: failed }))
  else ElMessage.success(t('monitors.batchDeleted', { count: ids.length }))
  await load()
}

onMounted(load)
</script>

<style scoped>
.monitor-summary {
  display: flex;
  gap: 12px;
  margin-bottom: 18px;
  flex-wrap: wrap;
}
.summary-item {
  flex: 1;
  min-width: 130px;
  background: #fff;
  border: 1px solid #e6eaf2;
  border-radius: 14px;
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.summary-num {
  font-size: 24px;
  font-weight: 700;
  letter-spacing: -0.03em;
}
.monitor-list {
  padding: 0;
  overflow: hidden;
}
.monitor-list-head,
.monitor-row {
  display: grid;
  grid-template-columns: minmax(0, 1.8fr) minmax(0, 1.5fr) 120px minmax(0, 1.2fr) 150px 232px;
  align-items: center;
  gap: 16px;
}
.monitor-list-head {
  padding: 12px 20px;
  background: #f8fafc;
  border-bottom: 1px solid var(--kp-border);
  color: var(--kp-muted);
  font-size: 12px;
  font-weight: 600;
}
.monitor-head-actions {
  text-align: right;
}
.monitor-row {
  padding: 14px 20px;
  border-bottom: 1px solid #f1f5f9;
  transition: background 0.15s ease;
}
.monitor-row:last-child {
  border-bottom: 0;
}
.monitor-row:hover {
  background: #f8fbff;
}
.monitor-row.is-selected {
  background: #f5f9ff;
}
.monitor-row.is-selected:hover {
  background: #eef5ff;
}
.monitor-head-name {
  display: flex;
  align-items: center;
  gap: 10px;
}
.monitor-head-name :deep(.el-checkbox) {
  height: auto;
  margin-right: 0;
}
.monitor-check {
  flex: none;
  margin-right: 0;
}
.batch-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 10px 14px;
  border: 1px solid #dbe7fb;
  border-radius: 12px;
  background: #f5f9ff;
}
.batch-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.batch-actions .el-button + .el-button {
  margin-left: 0;
}
.batch-count {
  font-size: 13px;
  font-weight: 600;
  color: #1d4ed8;
}
.monitor-cell {
  min-width: 0;
}
.monitor-name {
  display: flex;
  align-items: center;
  gap: 12px;
}
.monitor-name-text {
  min-width: 0;
}
.monitor-name-text .cred-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.monitor-icon {
  flex: none;
  width: 38px;
  height: 38px;
  border-radius: 12px;
  display: grid;
  place-items: center;
  font-size: 17px;
}
.dot-success {
  background: #ecfdf5;
  color: #059669;
}
.dot-warning {
  background: #fffbeb;
  color: #d97706;
}
.dot-danger {
  background: #fef2f2;
  color: #dc2626;
}
.dot-info {
  background: #eff6ff;
  color: #2563eb;
}
.monitor-strong {
  font-size: 13px;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.monitor-sub {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 3px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.monitor-ellipsis {
  display: block;
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.monitor-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
  flex-wrap: wrap;
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 6px;
}

@media (max-width: 1100px) {
  .monitor-list-head {
    display: none;
  }
  .monitor-row {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas:
      'name actions'
      'status status'
      'expires issuer'
      'next next';
    row-gap: 10px;
  }
  .monitor-name {
    grid-area: name;
  }
  .monitor-status {
    grid-area: status;
  }
  .monitor-expires {
    grid-area: expires;
  }
  .monitor-issuer {
    grid-area: issuer;
  }
  .monitor-next {
    grid-area: next;
  }
  .monitor-actions {
    grid-area: actions;
  }
}
</style>
