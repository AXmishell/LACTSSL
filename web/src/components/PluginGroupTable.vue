<template>
  <div>
    <div class="table-scroll">
      <el-table
        ref="tableRef"
        :data="visibleGroups"
        row-key="key"
        size="small"
        max-height="420"
        class="plugin-table"
        :empty-text="resolvedEmptyText"
        @selection-change="onSelectionChange"
      >
        <el-table-column
          type="selection"
          width="26"
          fixed="left"
          :reserve-selection="true"
          class-name="plugin-select-cell"
        />

        <el-table-column :label="t('plugins.pluginColumn')" min-width="260">
          <template #default="{ row }">
            <div class="plugin-name">{{ groupTitle(row) }}</div>
            <div class="plugin-meta">
              <span v-if="row.items.length === 1" class="mono" style="margin-right: 6px">{{ row.items[0].id }}</span>
              <el-tag
                v-if="row.file"
                size="small"
                :type="fileSourceType(row.file.source)"
                style="margin-right: 6px"
              >{{ fileSourceLabel(row.file.source) }}</el-tag>
              <el-tag v-else size="small" type="warning" style="margin-right: 6px">
                {{ t('plugins.fileUnmanaged') }}
              </el-tag>
              <el-tag
                v-if="row.file && !row.file.enabled"
                size="small"
                type="info"
                style="margin-right: 6px"
              >{{ t('plugins.fileDisabledTag') }}</el-tag>
              <span v-if="row.items.length > 1">{{ t('plugins.providedItems', { count: row.items.length }) }}</span>
              <span v-else-if="row.items.length === 1" class="mono">{{ fileLabel(row) }}</span>
              <span v-if="groupVersion(row)"> · {{ formatVersion(groupVersion(row)) }}</span>
              <el-button
                v-if="row.file || row.items.length > 1"
                link
                type="primary"
                size="small"
                class="plugin-manage-link"
                @click="openManage(row)"
              >{{ t('plugins.manageItems') }}</el-button>
            </div>
            <div v-if="!row.items.length" class="plugin-meta">{{ t('plugins.fileNoItems') }}</div>
          </template>
        </el-table-column>

        <el-table-column :label="t('plugins.fileSize')" width="80" align="center" class-name="plugin-size-cell">
          <template #default="{ row }">
            <span class="mono">{{ row.file ? formatSize(row.file.size) : '-' }}</span>
          </template>
        </el-table-column>

        <el-table-column :label="t('plugins.providerStatus')" width="58" align="center" class-name="plugin-status-cell">
          <template #default="{ row }">
            <div v-if="row.file" class="plugin-group-status" :title="fileToggleHint(row)">
              <el-switch
                :model-value="row.file.enabled"
                :loading="!!fileToggling[row.file.file]"
                :class="{ 'is-partial': row.file.enabled && enabledCount(row) < row.items.length }"
                @change="(value: any) => emit('toggle-file', row.file!, !!value)"
              />
              <span class="plugin-group-count mono">{{ enabledCount(row) }}/{{ row.items.length }}</span>
            </div>
            <div v-else-if="row.items.length === 1" class="plugin-group-status">
              <el-switch
                :model-value="row.items[0].enabled"
                :loading="!!toggling[row.items[0].id]"
                @change="(value: any) => emit('toggle', row.items[0], !!value)"
              />
            </div>
            <div
              v-else-if="row.items.length > 1"
              class="plugin-group-status"
              :title="t('plugins.groupToggleHint')"
            >
              <el-switch
                :model-value="allEnabled(row)"
                :loading="!!aggregateLoading[row.key]"
                :class="{ 'is-partial': isPartial(row) }"
                @change="(value: any) => emit('toggle-group', row, !!value)"
              />
              <span class="plugin-group-count mono">{{ enabledCount(row) }}/{{ row.items.length }}</span>
            </div>
            <span v-else class="sub">-</span>
          </template>
        </el-table-column>

        <el-table-column :label="t('plugins.repoAction')" width="50" align="center" class-name="plugin-action-cell">
          <template #default="{ row }">
            <el-button
              v-if="row.file"
              size="small"
              type="danger"
              plain
              :loading="!!deleting[row.file.file]"
              @click="emit('delete', row.file)"
            >{{ t('plugins.fileDelete') }}</el-button>
            <span v-else class="sub">-</span>
          </template>
        </el-table-column>
      </el-table>
    </div>
    <div v-if="showBatchBar && selectedGroups.length" class="plugin-batch-bar">
      <div class="plugin-batch-actions">
        <el-button
          size="small"
          type="primary"
          plain
          :loading="batchAction === 'enable'"
          @click="emit('batch-toggle', selectedGroups, true)"
        >{{ t('plugins.batchEnable') }}</el-button>
        <el-button
          size="small"
          :loading="batchAction === 'disable'"
          @click="emit('batch-toggle', selectedGroups, false)"
        >{{ t('plugins.batchDisable') }}</el-button>
        <el-button
          size="small"
          type="danger"
          plain
          :disabled="!selectedFiles.length"
          :loading="batchAction === 'delete'"
          @click="emit('batch-delete', selectedFiles)"
        >{{ t('plugins.batchDelete') }}</el-button>
        <el-button size="small" text @click="clearSelection">{{ t('plugins.clearSelection') }}</el-button>
      </div>
      <span class="plugin-batch-count">{{ t('plugins.selectedCount', { count: selectedGroups.length }) }}</span>
    </div>
    <div v-if="files.length" class="sub plugin-file-hint">{{ t('plugins.fileHint') }}</div>

    <el-drawer
      v-model="manageOpen"
      :title="t('plugins.manageItemsTitle')"
      size="620px"
      append-to-body
      destroy-on-close
      class="plugin-manage-drawer"
    >
      <template v-if="managedGroup">
        <div class="plugin-manage-head">
          <div>
            <div class="plugin-name">{{ groupTitle(managedGroup) }}</div>
            <div class="plugin-meta mono">{{ managedGroup.file?.file || managedGroup.key }}</div>
          </div>
          <div class="plugin-manage-head-side">
            <el-tag v-if="managedGroup.file" size="small" :type="fileSourceType(managedGroup.file.source)">
              {{ fileSourceLabel(managedGroup.file.source) }}
            </el-tag>
            <span v-if="groupVersion(managedGroup)" class="sub mono">{{ formatVersion(groupVersion(managedGroup)) }}</span>
            <el-switch
              v-if="managedGroup.file"
              :model-value="managedGroup.file.enabled"
              :loading="!!fileToggling[managedGroup.file.file]"
              @change="(value: any) => emit('toggle-file', managedGroup!.file!, !!value)"
            />
          </div>
        </div>

        <el-alert
          v-if="managedGroup.file && !managedGroup.file.enabled"
          :title="t('plugins.fileDisabledHint')"
          type="warning"
          show-icon
          :closable="false"
          class="plugin-manage-alert"
        >
          <el-button
            size="small"
            type="primary"
            plain
            :loading="!!fileToggling[managedGroup.file.file]"
            @click="emit('toggle-file', managedGroup!.file!, true)"
          >{{ t('plugins.fileEnable') }}</el-button>
        </el-alert>
        <el-alert
          v-if="conflictCount(managedGroup) > 0"
          :title="t('plugins.itemsConflictHint', { count: conflictCount(managedGroup) })"
          type="info"
          show-icon
          :closable="false"
          class="plugin-manage-alert"
        />

        <div class="plugin-manage-toolbar">
          <el-input
            v-model="manageFilter"
            :placeholder="t('plugins.searchItems')"
            clearable
            class="plugin-manage-search"
          />
          <div class="plugin-bulk">
            <el-button size="small" :disabled="!managedItems.length" @click="selectVisible">
              {{ t('plugins.selectAllFiltered') }}
            </el-button>
            <el-button
              size="small"
              type="primary"
              plain
              :disabled="!manageSelectedIDs.length || fileDisabled(managedGroup)"
              :loading="batchAction === 'enable'"
              @click="toggleSelected(true)"
            >{{ t('plugins.enableSelected') }}</el-button>
            <el-button
              size="small"
              :disabled="!manageSelectedIDs.length || fileDisabled(managedGroup)"
              :loading="batchAction === 'disable'"
              @click="toggleSelected(false)"
            >{{ t('plugins.disableSelected') }}</el-button>
            <el-button size="small" text :disabled="!manageSelectedIDs.length" @click="clearManageSelection">
              {{ t('plugins.clearSelection') }}
            </el-button>
          </div>
        </div>
        <div class="sub plugin-manage-count">
          {{ t('plugins.selectedItems', { count: manageSelectedIDs.length }) }} ·
          {{ t('plugins.enabledItems', { enabled: enabledCount(managedGroup), total: managedGroup.items.length }) }}
        </div>

        <div class="table-scroll">
          <el-table
            ref="manageTableRef"
            :data="managedItems"
            row-key="id"
            size="small"
            max-height="420"
            class="plugin-table plugin-manage-table"
            @selection-change="onManageSelection"
          >
            <el-table-column type="selection" width="34" class-name="plugin-select-cell" />
            <el-table-column :label="t('plugins.itemColumn')" min-width="260">
              <template #default="{ row }">
                <div class="plugin-name">{{ itemLabel(row) }}</div>
                <div class="plugin-meta mono">{{ row.id }}</div>
              </template>
            </el-table-column>
            <el-table-column :label="t('plugins.providerStatus')" width="70" align="center" class-name="plugin-status-cell">
              <template #default="{ row }">
                <el-switch
                  :model-value="row.enabled"
                  :disabled="fileDisabled(managedGroup)"
                  :loading="!!toggling[row.id]"
                  @change="(value: any) => emit('toggle', row, !!value)"
                />
              </template>
            </el-table-column>
          </el-table>
        </div>
        <div v-if="!managedItems.length" class="sub" style="padding: 12px 0">{{ t('plugins.noMatchedItems') }}</div>

        <div class="plugin-manage-footer">
          <el-button
            size="small"
            :disabled="!managedGroup.items.length || fileDisabled(managedGroup)"
            :loading="batchAction === 'enable'"
            @click="toggleAll(managedGroup, true)"
          >{{ t('plugins.enableAllItems') }}</el-button>
          <el-button
            size="small"
            :disabled="!managedGroup.items.length || fileDisabled(managedGroup)"
            :loading="batchAction === 'disable'"
            @click="toggleAll(managedGroup, false)"
          >{{ t('plugins.disableAllItems') }}</el-button>
        </div>
      </template>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessageBox } from 'element-plus'
import { useDelayedBatchBar } from '../batchBar'
import { notifyChannelLabelKey } from '../notifyChannels'
import { formatSize, formatVersion } from '../utils'
import type { PluginFile, PluginGroup, PluginItem } from '../types'

const props = withDefaults(defineProps<{
  kind: 'dns' | 'deploy' | 'notify'
  items: PluginItem[]
  files: PluginFile[]
  keyword?: string
  toggling?: Record<string, boolean>
  deleting?: Record<string, boolean>
  fileToggling?: Record<string, boolean>
  aggregateLoading?: Record<string, boolean>
  emptyText?: string
  batchAction?: '' | 'enable' | 'disable' | 'delete'
}>(), {
  keyword: '',
  toggling: () => ({}),
  deleting: () => ({}),
  fileToggling: () => ({}),
  aggregateLoading: () => ({}),
  emptyText: '',
  batchAction: ''
})

const emit = defineEmits<{
  toggle: [item: PluginItem, enabled: boolean]
  'toggle-group': [group: PluginGroup, enabled: boolean]
  'toggle-items': [items: PluginItem[], enabled: boolean]
  'toggle-file': [file: PluginFile, enabled: boolean]
  delete: [file: PluginFile]
  'batch-toggle': [groups: PluginGroup[], enabled: boolean]
  'batch-delete': [files: PluginFile[]]
}>()

const { t } = useI18n()

function helperName(helper?: string): string {
  return (helper || '').split(/[\\/]/).pop() || ''
}

// groups 把 helper 文件与它提供的条目合成一行。
// helper 是完整路径，文件表里只有文件名；同时用 manifest 的 items 兜底，
// 避免路径分隔符或大小写差异导致条目落到「未关联文件」分组。
const groups = computed<PluginGroup[]>(() => {
  const fileGroups: PluginGroup[] = props.files.map((file) => ({
    key: file.file,
    file,
    items: []
  }))
  const byFile = new Map(fileGroups.map((group) => [group.key, group]))
  const byItem = new Map<string, PluginGroup>()
  for (const group of fileGroups) {
    for (const id of group.file?.items || []) byItem.set(id, group)
  }
  const orphans = new Map<string, PluginGroup>()
  for (const item of props.items) {
    const name = helperName(item.helper)
    const group = (name && byFile.get(name)) || byItem.get(item.id)
    if (group) {
      group.items.push(item)
      continue
    }
    const key = name || item.id
    let orphan = orphans.get(key)
    if (!orphan) {
      orphan = { key, items: [] }
      orphans.set(key, orphan)
    }
    orphan.items.push(item)
  }
  return [...fileGroups, ...orphans.values()]
})

const visibleGroups = computed<PluginGroup[]>(() => {
  const keyword = props.keyword.trim().toLowerCase()
  if (!keyword) return groups.value
  return groups.value.filter((group) => {
    if (group.key.toLowerCase().includes(keyword)) return true
    if ((group.file?.id || '').toLowerCase().includes(keyword)) return true
    if ((group.file?.items || []).some((id) => id.toLowerCase().includes(keyword))) return true
    return group.items.some(
      (item) => item.id.toLowerCase().includes(keyword) || (item.name || '').toLowerCase().includes(keyword)
    )
  })
})

const resolvedEmptyText = computed(() => props.emptyText || t('plugins.noMatched'))

// 选择状态只保存 row key，数据刷新后自动映射到最新分组，避免批量操作引用过期对象。
const tableRef = ref<{ clearSelection: () => void } | null>(null)
const selectedKeys = ref<string[]>([])
const selectedGroups = computed(() => groups.value.filter((group) => selectedKeys.value.includes(group.key)))
const selectedFiles = computed(() =>
  selectedGroups.value.flatMap((group) => (group.file ? [group.file] : []))
)

function onSelectionChange(rows: PluginGroup[]): void {
  selectedKeys.value = rows.map((row) => row.key)
}

function clearSelection(): void {
  tableRef.value?.clearSelection()
  selectedKeys.value = []
}

const showBatchBar = useDelayedBatchBar(() => selectedKeys.value.length > 0)

// 二级条目管理：按分组 key 定位，父组件刷新数据后自动映射到最新对象。
const manageKey = ref('')
const manageFilter = ref('')
const manageSelectedIDs = ref<string[]>([])
const manageTableRef = ref<{ clearSelection: () => void; toggleRowSelection: (row: PluginItem, selected?: boolean) => void } | null>(null)
const managedGroup = computed(() => groups.value.find((group) => group.key === manageKey.value) || null)
const manageOpen = computed({
  get: () => !!managedGroup.value,
  set: (value: boolean) => {
    if (!value) manageKey.value = ''
  }
})
const managedItems = computed(() => {
  const group = managedGroup.value
  if (!group) return []
  const keyword = manageFilter.value.trim().toLowerCase()
  if (!keyword) return group.items
  return group.items.filter(
    (item) => item.id.toLowerCase().includes(keyword) || (item.name || '').toLowerCase().includes(keyword)
  )
})

watch(manageFilter, () => {
  manageTableRef.value?.clearSelection()
  manageSelectedIDs.value = []
})

function openManage(group: PluginGroup): void {
  manageKey.value = group.key
  manageFilter.value = ''
  manageSelectedIDs.value = []
}

function onManageSelection(rows: PluginItem[]): void {
  manageSelectedIDs.value = rows.map((row) => row.id)
}

function clearManageSelection(): void {
  manageTableRef.value?.clearSelection()
  manageSelectedIDs.value = []
}

function selectVisible(): void {
  manageTableRef.value?.clearSelection()
  const ids: string[] = []
  for (const item of managedItems.value) {
    manageTableRef.value?.toggleRowSelection(item, true)
    ids.push(item.id)
  }
  manageSelectedIDs.value = ids
}

function fileDisabled(group: PluginGroup | null): boolean {
  return !!group?.file && !group.file.enabled
}

function toggleSelected(enabled: boolean): void {
  const group = managedGroup.value
  if (!group) return
  const selected = new Set(manageSelectedIDs.value)
  const items = group.items.filter((item) => selected.has(item.id) && item.enabled !== enabled)
  if (items.length) emit('toggle-items', items, enabled)
}

async function toggleAll(group: PluginGroup, enabled: boolean): Promise<void> {
  const items = group.items.filter((item) => item.enabled !== enabled)
  if (!items.length) return
  if (enabled && items.length > 10) {
    try {
      await ElMessageBox.confirm(
        t('plugins.allItemsConfirm', { name: groupTitle(group), count: items.length }),
        t('plugins.allItemsConfirmTitle'),
        { type: 'warning', confirmButtonText: t('plugins.enableAllItems'), cancelButtonText: t('common.cancel') }
      )
    } catch {
      return
    }
  }
  emit('toggle-items', items, enabled)
}

function itemLabel(item: PluginItem): string {
  if (props.kind === 'notify') {
    const key = notifyChannelLabelKey(item.id)
    if (key) return t(key)
  }
  return item.name || item.id
}

function groupTitle(group: PluginGroup): string {
  if (group.items.length === 1) return itemLabel(group.items[0])
  return group.file?.file || group.key
}

function fileLabel(group: PluginGroup): string {
  return group.file?.file || group.key
}

function groupVersion(group: PluginGroup): string {
  return group.file?.version || group.items[0]?.version || ''
}

function enabledCount(group: PluginGroup): number {
  return group.items.filter((item) => item.enabled).length
}

function allEnabled(group: PluginGroup): boolean {
  return group.items.length > 0 && group.items.every((item) => item.enabled)
}

function isPartial(group: PluginGroup): boolean {
  const enabled = enabledCount(group)
  return enabled > 0 && enabled < group.items.length
}

function conflictCount(group: PluginGroup): number {
  const declared = group.file?.items?.length || 0
  return Math.max(0, declared - group.items.length)
}

function fileToggleHint(group: PluginGroup): string {
  return group.file && !group.file.enabled
    ? t('plugins.fileToggleHintDisabled')
    : t('plugins.fileToggleHint')
}

function fileSourceLabel(source: PluginFile['source']): string {
  if (source === 'repo') return t('plugins.fileSourceRepo')
  if (source === 'legacy') return t('plugins.fileSourceLegacy')
  return t('plugins.fileSourceManual')
}

function fileSourceType(source: PluginFile['source']): 'success' | 'warning' | 'info' {
  if (source === 'repo') return 'success'
  if (source === 'legacy') return 'warning'
  return 'info'
}
</script>

<style scoped>
.plugin-table {
  margin-top: 10px;
}
.plugin-name {
  font-weight: 500;
}
.plugin-meta {
  margin-top: 2px;
  font-size: 12px;
  opacity: 0.65;
  word-break: break-all;
}
.plugin-manage-link {
  margin-left: 6px;
  vertical-align: baseline;
}
.plugin-group-status {
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  white-space: nowrap;
}
.plugin-group-count {
  font-size: 10px;
  line-height: 1;
  opacity: 0.65;
}
/* 状态列 58px：取消内边距，让开关和计数都能放进窄列。 */
.plugin-table :deep(.plugin-status-cell .cell) {
  padding: 0;
}
.plugin-file-hint {
  margin-top: 8px;
}
/* 多选列：复选框 12px，右侧保留 14px 外边距；取消单元格内边距，贴齐列边缘。 */
.plugin-table :deep(.plugin-select-cell .cell) {
  padding: 0;
  justify-content: center;
}
.plugin-table :deep(.plugin-select-cell .el-checkbox) {
  margin-right: 14px;
}
/* 操作列取消单元格左右内边距，让窄列下的删除按钮不被挤压。 */
.plugin-table :deep(.plugin-action-cell .cell) {
  padding: 0;
}
/* 大小列同样取消内边距并保持单行，避免 9.1 MB 在 50px 列宽里换行。 */
.plugin-table :deep(.plugin-size-cell .cell) {
  padding: 0;
  white-space: nowrap;
}
/* 多条目插件显示聚合开关；部分启用时用警告色提示，右侧同时显示 N/M。 */
.plugin-group-status :deep(.el-switch.is-partial .el-switch__core) {
  background-color: var(--el-color-warning);
  border-color: var(--el-color-warning);
}
.plugin-batch-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 10px;
  padding: 10px 14px;
  border: 1px solid var(--el-color-primary-light-7);
  border-radius: 10px;
  background: var(--el-color-primary-light-9);
  animation: plugin-batch-bar-in 0.18s ease-out;
}
@keyframes plugin-batch-bar-in {
  from {
    opacity: 0;
    transform: translateY(-4px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}
.plugin-batch-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.plugin-batch-actions .el-button + .el-button {
  margin-left: 0;
}
.plugin-batch-count {
  font-size: 13px;
  font-weight: 600;
  color: var(--el-color-primary);
}
.plugin-manage-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.plugin-manage-head-side {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: flex-end;
}
.plugin-manage-alert {
  margin-top: 12px;
}
.plugin-manage-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 14px;
}
.plugin-manage-search {
  max-width: 220px;
}
.plugin-manage-count {
  margin-top: 8px;
}
.plugin-manage-table {
  margin-top: 8px;
}
.plugin-manage-footer {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}
.plugin-manage-footer .el-button + .el-button {
  margin-left: 0;
}
</style>
