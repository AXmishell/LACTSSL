<template>
  <div class="page table-page">
    <div class="page-head">
      <div>
        <h1>{{ t('apiKeys.title') }}</h1>
        <p>{{ t('apiKeys.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle :loading="loading" @click="load" />
        <el-button type="primary" :icon="Plus" @click="openCreate">{{ t('apiKeys.create') }}</el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      :title="t('apiKeys.intro')"
      style="margin-bottom: 16px"
    />

    <div class="k-card table-fill" style="padding: 0; overflow: hidden">
      <div class="table-scroll">
        <el-table
          ref="tableRef"
          :data="items"
          row-key="id"
          v-loading="loading"
          height="100%"
          style="width: 100%"
          @selection-change="onSelectionChange"
        >
          <el-table-column type="selection" width="48" fixed="left" :reserve-selection="true" />
          <el-table-column width="150" :label="t('apiKeys.name')">
            <template #default="{ row }">
              <div class="key-name">{{ row.name }}</div>
              <div class="key-prefix mono">{{ row.prefix }}…</div>
            </template>
          </el-table-column>
          <el-table-column min-width="240" :label="t('apiKeys.permissions')">
            <template #default="{ row }">
              <div class="scope-tags">
                <el-tooltip
                  v-for="scope in row.scopes.slice(0, 3)"
                  :key="scope"
                  :content="scopeDescription(scope)"
                  placement="top"
                >
                  <el-tag size="small" :type="scope === '*' ? 'danger' : 'info'" effect="plain">
                    {{ scopeLabel(scope) }}
                  </el-tag>
                </el-tooltip>
                <el-tag v-if="row.scopes.length > 3" size="small" type="info" effect="plain">
                  +{{ row.scopes.length - 3 }}
                </el-tag>
                <el-tooltip v-if="sensitiveOf(row).length" :content="sensitiveOf(row).join('、')" placement="top">
                  <el-tag size="small" type="warning" effect="plain">
                    {{ t('apiKeys.highRiskTag', { count: sensitiveOf(row).length }) }}
                  </el-tag>
                </el-tooltip>
              </div>
            </template>
          </el-table-column>
          <el-table-column width="80" :label="t('common.status')">
            <template #default="{ row }">
              <span class="tag-soft" :class="statusOf(row).className">{{ statusOf(row).label }}</span>
            </template>
          </el-table-column>
          <el-table-column width="80" :label="t('apiKeys.lastUsed')">
            <template #default="{ row }">
              <span v-if="row.last_used_at">{{ fromNow(row.last_used_at) }}</span>
              <span v-else class="muted">{{ t('apiKeys.neverUsed') }}</span>
            </template>
          </el-table-column>
          <el-table-column width="80" :label="t('apiKeys.expiresAt')">
            <template #default="{ row }">
              <span :class="{ muted: !row.expires_at }">{{ row.expires_at ? formatDate(row.expires_at) : t('apiKeys.neverExpires') }}</span>
            </template>
          </el-table-column>
          <el-table-column width="120" :label="t('apiKeys.createdAt')">
            <template #default="{ row }">
              <span class="muted">{{ formatDate(row.created_at) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="220" align="right" :label="t('common.actions')">
            <template #default="{ row }">
              <el-button text type="primary" size="small" @click="openEdit(row)">{{ t('common.edit') }}</el-button>
              <el-button text size="small" @click="rotate(row)">{{ t('apiKeys.rotate') }}</el-button>
              <el-button text type="danger" size="small" @click="remove(row)">{{ t('common.delete') }}</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div style="padding: 40px 0; color: #94a3b8">{{ t('apiKeys.empty') }}</div>
          </template>
        </el-table>
      </div>
      <div class="flex-between key-foot">
        <span class="muted">{{ t('apiKeys.total', { count: items.length }) }}</span>
      </div>
    </div>

    <div v-if="showBatchBar && selectedIds.length" class="batch-bar">
      <div class="batch-actions">
        <el-button size="small" type="danger" plain :icon="Delete" :loading="batchDeleting" @click="batchRemove">
          {{ t('apiKeys.batchDelete') }}
        </el-button>
        <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('apiKeys.clearSelection') }}</el-button>
      </div>
      <span class="batch-count">{{ t('apiKeys.batchSelected', { count: selectedIds.length }) }}</span>
    </div>

    <!-- 创建 / 编辑 -->
    <el-dialog
      v-model="formVisible"
      :title="editing ? t('apiKeys.editTitle') : t('apiKeys.createTitle')"
      width="min(920px, 94vw)"
      top="4vh"
      :close-on-click-modal="false"
    >
      <el-form label-position="top">
        <div class="form-row">
          <el-form-item :label="t('apiKeys.nameLabel')" required class="form-row-name">
            <el-input v-model="form.name" maxlength="64" :placeholder="t('apiKeys.namePlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('apiKeys.expiresLabel')">
            <el-date-picker
              v-model="form.expiresAt"
              type="datetime"
              :placeholder="t('apiKeys.expiresPlaceholder')"
              :disabled-date="disabledDate"
              style="width: 100%"
            />
            <div class="form-help">{{ t('apiKeys.expiresHelp') }}</div>
          </el-form-item>
        </div>
        <el-form-item v-if="editing" :label="t('apiKeys.enabledLabel')">
          <el-switch v-model="form.enabled" />
          <span class="form-help" style="margin-left: 10px">{{ t('apiKeys.enabledHelp') }}</span>
        </el-form-item>
        <el-form-item :label="t('apiKeys.permissions')" required>
          <div class="scope-toolbar">
            <el-input
              v-model="scopeQuery"
              :prefix-icon="Search"
              clearable
              size="small"
              :placeholder="t('apiKeys.scopeSearch')"
              class="scope-search"
            />
            <el-checkbox v-model="showSelectedOnly" size="small">{{ t('apiKeys.scopeFilterSelected') }}</el-checkbox>
            <el-checkbox v-model="showSensitiveOnly" size="small">{{ t('apiKeys.scopeFilterSensitive') }}</el-checkbox>
            <el-checkbox v-model="showDesc" size="small">{{ t('apiKeys.scopeShowDesc') }}</el-checkbox>
          </div>

          <div class="scope-picker" :class="{ 'has-desc': showDesc, 'is-locked': isAll }">
            <div v-if="allScope && matchesFilter(allScope)" class="scope-all">
              <el-checkbox :model-value="isAll" class="scope-all-check" @change="toggleAll">
                <span class="scope-label">
                  <el-icon class="scope-all-icon"><WarningFilled /></el-icon>
                  <span class="scope-name">{{ allScope.label }}</span>
                </span>
              </el-checkbox>
              <p class="scope-all-desc">{{ allScope.description }}</p>
            </div>

            <div v-for="module in visibleModules" :key="module.value" class="scope-group">
              <div class="scope-group-head">
                <el-checkbox
                  :model-value="module.allSelected"
                  :indeterminate="module.someSelected"
                  :disabled="isAll"
                  @change="toggleModule(module)"
                >
                  {{ module.label }}
                </el-checkbox>
                <span class="scope-group-count">{{ module.selectedCount }}/{{ module.scopes.length }}</span>
              </div>
              <el-checkbox-group v-model="form.scopes" class="scope-grid" @change="handleScopeChange">
                <el-checkbox
                  v-for="scope in module.scopes"
                  :key="scope.value"
                  :value="scope.value"
                  :disabled="isAll"
                  class="scope-checkbox"
                >
                  <el-tooltip
                    :content="scope.description"
                    placement="top"
                    :show-after="300"
                    :disabled="showDesc || !scope.description"
                    popper-class="scope-tip"
                  >
                    <span class="scope-label">
                      <span v-if="scope.sensitive" class="scope-risk-dot" :aria-label="t('apiKeys.sensitive')" />
                      <span class="scope-name">{{ scope.label }}</span>
                      <span v-if="showDesc" class="scope-desc">{{ scope.description }}</span>
                    </span>
                  </el-tooltip>
                </el-checkbox>
              </el-checkbox-group>
            </div>

            <p v-if="!visibleModules.length && !isAll" class="scope-empty">{{ t('apiKeys.scopeEmpty') }}</p>
            <p v-else-if="isAll" class="scope-lock-hint">{{ t('apiKeys.scopeLockedHint') }}</p>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="scope-footer">
          <span class="scope-footer-summary">
            <template v-if="isAll">{{ t('apiKeys.scopeAllSelected') }}</template>
            <template v-else>
              {{ t('apiKeys.selectedCount', { count: form.scopes.length }) }}
              <el-tooltip v-if="selectedSensitive.length" :content="selectedSensitive.join('、')" placement="top">
                <span class="scope-footer-risk">{{ t('apiKeys.selectedSensitive', { count: selectedSensitive.length }) }}</span>
              </el-tooltip>
            </template>
          </span>
          <span class="scope-footer-actions">
            <el-button @click="formVisible = false">{{ t('common.cancel') }}</el-button>
            <el-button type="primary" :loading="saving" @click="save">{{ t('common.save') }}</el-button>
          </span>
        </div>
      </template>
    </el-dialog>

    <!-- 仅显示一次的令牌 -->
    <el-dialog v-model="tokenVisible" :title="t('apiKeys.tokenTitle')" width="620px" :close-on-click-modal="false" @closed="issuedToken = ''">
      <el-alert type="warning" :closable="false" show-icon :title="t('apiKeys.tokenWarning')" style="margin-bottom: 16px" />
      <div class="token-box mono">{{ issuedToken }}</div>
      <div class="flex gap-10" style="margin: 12px 0 18px">
        <el-button type="primary" plain :icon="CopyDocument" @click="copyToken">{{ t('apiKeys.copyToken') }}</el-button>
      </div>
      <template #footer>
        <el-button type="primary" @click="tokenVisible = false">{{ t('apiKeys.savedToken') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Close, CopyDocument, Delete, Plus, Refresh, Search, WarningFilled } from '@element-plus/icons-vue'
import { api, copyText } from '../api'
import { useDelayedBatchBar } from '../batchBar'
import { SCOPE_ALL_GROUP, SCOPE_MODULES, moduleOfGroup } from '../scopeCatalog'
import { formatDate, fromNow } from '../utils'
import type { APIKey, APIKeyCreateResult, APIKeyScope, APIKeyScopeGroup } from '../types'

interface ScopeModuleView {
  value: string
  label: string
  scopes: APIKeyScope[]
  selectedCount: number
  allSelected: boolean
  someSelected: boolean
}

const DESC_PREF_KEY = 'lactssl.apiKeyScopeShowDesc'

function readDescPreference(): boolean {
  try {
    return localStorage.getItem(DESC_PREF_KEY) === '1'
  } catch {
    return false
  }
}

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const items = ref<APIKey[]>([])
const scopes = ref<APIKeyScope[]>([])
const tableRef = ref<{ clearSelection: () => void } | null>(null)
const selectedIds = ref<number[]>([])
const batchDeleting = ref(false)
const showBatchBar = useDelayedBatchBar(() => selectedIds.value.length > 0)

const formVisible = ref(false)
const editing = ref<APIKey | null>(null)
const form = reactive({
  name: '',
  scopes: [] as string[],
  expiresAt: null as Date | null,
  enabled: true
})

const tokenVisible = ref(false)
const issuedToken = ref('')

const scopeMap = computed(() => {
  const map = new Map<string, APIKeyScope>()
  for (const scope of scopes.value) map.set(scope.value, scope)
  return map
})

const scopeQuery = ref('')
const showSelectedOnly = ref(false)
const showSensitiveOnly = ref(false)
const showDesc = ref(readDescPreference())

watch(showDesc, (value) => {
  try {
    localStorage.setItem(DESC_PREF_KEY, value ? '1' : '0')
  } catch {
    // 隐私模式下 localStorage 不可用时忽略即可。
  }
})

const allScope = computed(() => scopes.value.find((scope) => scope.group === SCOPE_ALL_GROUP) || null)
const isAll = computed(() => form.scopes.includes('*'))

function matchesFilter(scope: APIKeyScope): boolean {
  if (showSelectedOnly.value && !form.scopes.includes(scope.value)) return false
  if (showSensitiveOnly.value && !scope.sensitive) return false
  const query = scopeQuery.value.trim().toLowerCase()
  if (!query) return true
  return [scope.label, scope.description, scope.value].some((text) => text.toLowerCase().includes(query))
}

// 后端用 17 个分组描述权限，这里按资源域收敛成 6 个模块，避免大量只有 1-2 项的分组头。
const visibleModules = computed<ScopeModuleView[]>(() => {
  const buckets = new Map<string, APIKeyScope[]>()
  for (const scope of scopes.value) {
    if (scope.group === SCOPE_ALL_GROUP || !matchesFilter(scope)) continue
    const key = moduleOfGroup(scope.group)
    const list = buckets.get(key)
    if (list) list.push(scope)
    else buckets.set(key, [scope])
  }
  const defs = [...SCOPE_MODULES, { value: 'other', labelKey: 'apiKeys.moduleOther', groups: [] }]
  return defs
    .map((def) => {
      const list = buckets.get(def.value) || []
      const selectedCount = list.filter((scope) => form.scopes.includes(scope.value)).length
      return {
        value: def.value,
        label: t(def.labelKey),
        scopes: list,
        selectedCount,
        allSelected: list.length > 0 && selectedCount === list.length,
        someSelected: selectedCount > 0 && selectedCount < list.length
      }
    })
    .filter((module) => module.scopes.length > 0)
})

const selectedSensitive = computed(() =>
  form.scopes
    .map((value) => scopeMap.value.get(value))
    .filter((scope): scope is APIKeyScope => !!scope && scope.sensitive && scope.value !== '*')
    .map((scope) => scope.label)
)

function scopeLabel(value: string): string {
  return scopeMap.value.get(value)?.label || value
}

function scopeDescription(value: string): string {
  return scopeMap.value.get(value)?.description || value
}

// 表格里集中提示“这把钥匙带几项高危能力”，避免用户逐个查看 tag。
function sensitiveOf(key: APIKey): string[] {
  return key.scopes
    .filter((value) => value !== '*')
    .map((value) => scopeMap.value.get(value))
    .filter((scope): scope is APIKeyScope => !!scope && scope.sensitive)
    .map((scope) => scope.label)
}

function statusOf(key: APIKey): { label: string; className: string } {
  if (key.expires_at && new Date(key.expires_at) <= new Date()) {
    return { label: t('apiKeys.statusExpired'), className: 'tag-red' }
  }
  if (!key.enabled) {
    return { label: t('apiKeys.statusDisabled'), className: 'tag-gray' }
  }
  return { label: t('apiKeys.statusEnabled'), className: 'tag-green' }
}

function disabledDate(date: Date): boolean {
  return date.getTime() < Date.now() - 24 * 60 * 60 * 1000
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [list, scopeData] = await Promise.all([
      api.get<APIKey[]>('/api-keys'),
      scopes.value.length ? Promise.resolve(null) : api.get<{ scopes: APIKeyScope[]; groups: APIKeyScopeGroup[] }>('/api-keys/scopes')
    ])
    items.value = list
    if (scopeData) {
      scopes.value = scopeData.scopes
    }
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editing.value = null
  form.name = ''
  form.scopes = []
  form.expiresAt = null
  form.enabled = true
  scopeQuery.value = ''
  formVisible.value = true
}

function openEdit(key: APIKey): void {
  editing.value = key
  form.name = key.name
  form.scopes = [...key.scopes]
  form.expiresAt = key.expires_at ? new Date(key.expires_at) : null
  form.enabled = key.enabled
  scopeQuery.value = ''
  formVisible.value = true
}

function toggleAll(): void {
  form.scopes = isAll.value ? [] : ['*']
}

function toggleModule(module: ScopeModuleView): void {
  const values = module.scopes.map((scope) => scope.value)
  if (module.allSelected) {
    form.scopes = form.scopes.filter((scope) => !values.includes(scope))
    return
  }
  form.scopes = Array.from(new Set([...form.scopes.filter((scope) => scope !== '*'), ...values]))
}

function handleScopeChange(): void {
  // 全权限已经隐含所有能力，保留其他勾选只会让界面产生歧义。
  if (form.scopes.includes('*') && form.scopes.length > 1) {
    form.scopes = ['*']
  }
}

function payload() {
  return {
    name: form.name.trim(),
    scopes: form.scopes,
    enabled: form.enabled,
    expires_at: form.expiresAt ? form.expiresAt.toISOString() : ''
  }
}

async function save(): Promise<void> {
  if (!form.name.trim()) {
    ElMessage.warning(t('apiKeys.nameRequired'))
    return
  }
  if (!form.scopes.length) {
    ElMessage.warning(t('apiKeys.scopesRequired'))
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      const result = await api.put<APIKey>(`/api-keys/${editing.value!.id}`, payload())
      if (result === null) return
      ElMessage.success(t('apiKeys.updated'))
      formVisible.value = false
      await load()
      return
    }
    const result = await api.post<APIKeyCreateResult>('/api-keys', payload())
    if (result === null) return
    ElMessage.success(t('apiKeys.created'))
    formVisible.value = false
    showToken(result.token)
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function rotate(key: APIKey): Promise<void> {
  try {
    await ElMessageBox.confirm(t('apiKeys.rotateConfirm', { name: key.name }), t('apiKeys.rotateTitle'), {
      confirmButtonText: t('apiKeys.rotate'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
    const result = await api.post<APIKeyCreateResult>(`/api-keys/${key.id}/rotate`, undefined)
    if (result === null) return
    ElMessage.success(t('apiKeys.rotated'))
    showToken(result.token)
    await load()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.message)
  }
}

async function remove(key: APIKey): Promise<void> {
  try {
    await ElMessageBox.confirm(t('apiKeys.deleteConfirm', { name: key.name }), t('apiKeys.deleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
    const result = await api.delete(`/api-keys/${key.id}`)
    if (result === null) return
    ElMessage.success(t('apiKeys.deleted'))
    await load()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.message)
  }
}

function onSelectionChange(rows: APIKey[]): void {
  selectedIds.value = rows.map((row) => row.id)
}

function clearSelection(): void {
  tableRef.value?.clearSelection()
  selectedIds.value = []
}

async function batchRemove(): Promise<void> {
  const list = [...selectedIds.value]
  if (!list.length) return
  try {
    await ElMessageBox.confirm(t('apiKeys.batchDeleteConfirm', { count: list.length }), t('apiKeys.batchDeleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  batchDeleting.value = true
  let failed = 0
  try {
    for (const id of list) {
      try {
        await api.delete(`/api-keys/${id}`)
      } catch (error: any) {
        failed++
        ElMessage.error(error.message)
      }
    }
  } finally {
    batchDeleting.value = false
  }
  if (failed) ElMessage.warning(t('apiKeys.batchDeleteFailed', { count: failed }))
  else ElMessage.success(t('apiKeys.batchDeleted', { count: list.length }))
  clearSelection()
  await load()
}

function showToken(token: string): void {
  issuedToken.value = token
  tokenVisible.value = true
}

async function copyToken(): Promise<void> {
  if (await copyText(issuedToken.value)) {
    ElMessage.success(t('apiKeys.tokenCopied'))
  } else {
    ElMessage.warning(t('apiKeys.tokenCopyFailed'))
  }
}

onMounted(load)
</script>

<style scoped>
.key-name {
  font-weight: 600;
}

.key-prefix {
  margin-top: 4px;
  font-size: 12px;
  color: #94a3b8;
}

.scope-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.key-foot {
  padding: 12px 16px 16px;
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

/* 名称与过期时间并排，省下约 70px 高度留给权限矩阵。 */
.form-row {
  display: flex;
  gap: 16px;
}

.form-row :deep(.el-form-item) {
  flex: 1;
  min-width: 0;
}

.form-row-name {
  flex: 1.5;
}

.scope-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
  width: 100%;
  margin-bottom: 10px;
}

.scope-search {
  width: 200px;
}

.scope-toolbar :deep(.el-checkbox) {
  height: 24px;
  margin-right: 0;
}

/* 三列瀑布流：按模块自然分栏，模块内部不拆断，避免整页纵向滚动。 */
.scope-picker {
  width: 100%;
  line-height: 1.5;
  column-count: 3;
  column-gap: 20px;
}

.scope-picker.has-desc {
  column-count: 2;
}

.scope-picker.is-locked .scope-group {
  opacity: 0.5;
}

.scope-all {
  column-span: all;
  margin-bottom: 12px;
  padding: 10px 12px;
  border: 1px solid #fed7aa;
  border-radius: 8px;
  background: #fff7ed;
}

.scope-all-check {
  display: flex;
  height: auto;
}

.scope-all-check :deep(.el-checkbox__label) {
  padding-left: 8px;
  line-height: 20px;
}

.scope-all .scope-label {
  font-weight: 600;
  color: #b45309;
}

.scope-all-icon {
  color: #d97706;
}

.scope-all-desc {
  margin: 4px 0 0 24px;
  font-size: 12px;
  line-height: 1.5;
  color: #b45309;
}

.scope-group {
  break-inside: avoid;
  margin-bottom: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid #f1f5f9;
}

.scope-group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 2px;
}

.scope-group-head :deep(.el-checkbox) {
  height: auto;
  margin-right: 0;
}

.scope-group-head :deep(.el-checkbox__label) {
  font-size: 13px;
  font-weight: 600;
  color: #475569;
}

.scope-group-count {
  font-size: 11px;
  color: #94a3b8;
  font-variant-numeric: tabular-nums;
}

.scope-grid {
  display: block;
}

.scope-checkbox {
  display: flex;
  align-items: flex-start;
  width: 100%;
  height: auto;
  margin: 1px 0;
  white-space: normal;
}

.scope-checkbox :deep(.el-checkbox__label) {
  padding-left: 8px;
  line-height: 20px;
}

.scope-label {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 6px;
  max-width: 100%;
  font-size: 13px;
  font-weight: 400;
  line-height: 20px;
  color: #1e293b;
}

.scope-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.scope-risk-dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #f59e0b;
}

.scope-desc {
  flex: 0 0 100%;
  font-size: 12px;
  line-height: 16px;
  color: #94a3b8;
}

.scope-empty,
.scope-lock-hint {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: #94a3b8;
}

.scope-lock-hint {
  color: #d97706;
}

.scope-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  text-align: left;
}

.scope-footer-summary {
  font-size: 12px;
  color: #64748b;
}

.scope-footer-risk {
  margin-left: 4px;
  font-weight: 600;
  color: #d97706;
  cursor: help;
}

.scope-footer-actions {
  display: flex;
  gap: 8px;
  margin-left: auto;
}

@media (max-width: 1100px) {
  .scope-picker,
  .scope-picker.has-desc {
    column-count: 2;
  }
}

@media (max-width: 720px) {
  .scope-picker,
  .scope-picker.has-desc {
    column-count: 1;
  }

  .scope-search {
    width: 100%;
  }

  .form-row {
    display: block;
  }
}

/* tooltip 被 teleport 到 body，需要全局类名约束换行宽度。 */
:global(.scope-tip) {
  max-width: 320px;
  white-space: normal;
  line-height: 1.6;
}

.token-box {
  padding: 12px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
  color: #0f172a;
  word-break: break-all;
  font-size: 13px;
}

</style>
