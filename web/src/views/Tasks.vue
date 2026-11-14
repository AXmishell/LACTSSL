<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('tasks.title') }}</h1>
        <p>{{ t('tasks.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle @click="loadAll" />
        <el-button type="primary" :icon="Plus" @click="openCreate">{{ t('tasks.newTask') }}</el-button>
      </div>
    </div>

    <div class="k-card task-list" style="padding: 0; overflow: hidden">
      <el-table
        ref="tableRef"
        :data="tasks"
        row-key="id"
        v-loading="loading"
        style="width: 100%"
        @selection-change="onSelectionChange"
      >
        <el-table-column type="selection" width="48" fixed="left" :reserve-selection="true" />
        <el-table-column min-width="230" :label="t('tasks.task')">
          <template #default="{ row }">
            <div class="domain-cell">
              <span class="domain-main">{{ row.name }}</span>
              <span class="domain-sub">{{ shortDomains(row.domains, 3) }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column width="140" :label="t('tasks.challenge')">
          <template #default>
            <span class="tag-soft tag-blue">DNS-01</span>
          </template>
        </el-table-column>
        <el-table-column width="140" :label="t('tasks.ca')">
          <template #default="{ row }">
            <span class="tag-soft tag-gray">{{ caLabel(row.ca) }}</span>
          </template>
        </el-table-column>
        <el-table-column width="190" :label="t('tasks.pipeline')">
          <template #default="{ row }">
            <div class="pipeline-summary">
              <span class="pipeline-summary-text">{{ pipelineSummary(row) }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column width="120" :label="t('tasks.status')">
          <template #default="{ row }">
            <el-tooltip :content="row.message || t('tasks.noExecutionInfo')" placement="top" :disabled="!row.message">
              <el-tag :type="taskState(row.status).type" effect="light" round size="small">{{ taskState(row.status).label }}</el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column width="190" :label="t('tasks.execTime')">
          <template #default="{ row }">
            <div class="domain-cell">
              <span class="domain-sub">{{ t('tasks.lastRun', { value: row.last_run_at ? fromNow(row.last_run_at) : t('tasks.neverRun') }) }}</span>
              <span class="domain-sub">{{ t('tasks.nextRun', { value: row.next_run_at ? formatDate(row.next_run_at) : t('tasks.notScheduled') }) }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column width="240" :label="t('common.actions')" fixed="right">
          <template #default="{ row }">
            <el-button text type="primary" size="small" :loading="row.status === 'running'" @click="runTask(row)">{{ t('tasks.run') }}</el-button>
            <el-button text size="small" @click="openLogs(row)">{{ t('tasks.logs') }}</el-button>
            <el-dropdown trigger="click" @command="(cmd: string) => handleMore(cmd, row)">
              <el-button text size="small">{{ t('tasks.more') }}<el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="edit">{{ t('tasks.editTask') }}</el-dropdown-item>
                  <el-dropdown-item command="toggle">{{ row.enabled ? t('tasks.pauseAutoRenew') : t('tasks.enableAutoRenew') }}</el-dropdown-item>
                  <el-dropdown-item command="delete" divided>{{ t('tasks.deleteTask') }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>
        <template #empty>
          <div style="padding: 40px 0; color: #94a3b8">
            <el-icon size="30"><Promotion /></el-icon>
            <p>{{ t('tasks.empty') }}</p>
          </div>
        </template>
      </el-table>
    </div>

    <div v-if="selectedTasks.length" class="batch-bar">
      <div class="batch-actions">
        <el-button size="small" type="primary" plain :icon="VideoPlay" :loading="batchRunning" @click="batchRun">
          {{ t('tasks.batchRun') }}
        </el-button>
        <el-button size="small" :icon="VideoPause" :loading="batchToggling" @click="batchSetEnabled(false)">
          {{ t('tasks.batchPause') }}
        </el-button>
        <el-button size="small" :icon="VideoPlay" :loading="batchToggling" @click="batchSetEnabled(true)">
          {{ t('tasks.batchEnable') }}
        </el-button>
        <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('tasks.clearSelection') }}</el-button>
      </div>
      <span class="batch-count">{{ t('tasks.selectedCount', { count: selectedTasks.length }) }}</span>
    </div>

    <!-- 新建/编辑：任务流程编辑器 -->
    <el-dialog
      v-model="dialogVisible"
      :title="editingId ? t('tasks.editTitle') : t('tasks.createTitle')"
      width="1080px"
      top="4vh"
      destroy-on-close
      class="task-pipeline-dialog"
    >
      <div class="dialog-head">
        <div class="dialog-head-main">
          <el-input v-model="form.name" :placeholder="t('tasks.namePlaceholder')" maxlength="60" clearable />
          <span class="dialog-hint">{{ t('tasks.pipelineHint') }}</span>
        </div>
        <div class="dialog-enable">
          <span class="muted">{{ form.enabled ? t('tasks.enabled') : t('tasks.paused') }}</span>
          <el-switch v-model="form.enabled" />
        </div>
      </div>

      <div class="pipeline-editor">
        <aside class="pipeline-flow">
          <div class="pipeline-flow-head">
            <div>
              <div class="pipeline-flow-title">{{ t('tasks.pipeline') }}</div>
              <div class="pipeline-flow-sub">{{ t('tasks.nodeCount', { count: form.pipeline.length }) }}</div>
            </div>
            <el-dropdown trigger="click" @command="addPipelineNode">
              <el-button size="small" type="primary" plain :icon="Plus">{{ t('tasks.addNode') }}</el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="deploy">{{ t('tasks.deployNode') }}</el-dropdown-item>
                  <el-dropdown-item command="notify">{{ t('tasks.notifyNode') }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>

          <div ref="pipelineListRef" class="pipeline-list" @dragstart.prevent>
            <TransitionGroup
              name="pipeline"
              tag="div"
              class="pipeline-track"
              :class="{ 'is-dragging': draggingId, 'is-settling': settling }"
            >
              <div
                v-for="(node, index) in form.pipeline"
                :key="node.id"
                class="pipeline-item"
                :class="{ 'is-dragging': draggingId === node.id }"
                :style="itemShiftStyle(node.id)"
              >
                <div v-if="index > 0" class="pipeline-connector" aria-hidden="true">
                  <span class="pipeline-connector-line"></span>
                  <span class="pipeline-connector-dot"></span>
                </div>
                <div
                  class="pipeline-node"
                  :class="{
                    active: selectedNodeId === node.id,
                    muted: !node.enabled
                  }"
                  @click="selectNode(node)"
                >
                  <div
                    v-if="node.type !== 'apply'"
                    class="drag-handle"
                    :title="t('tasks.dragHint')"
                    @pointerdown.stop.prevent="onHandlePointerDown($event, index, node)"
                    @click.stop
                  >
                    <el-icon><Rank /></el-icon>
                  </div>
                  <div class="pipeline-node-icon" :class="`type-${node.type}`">
                    <el-icon>
                      <DocumentAdd v-if="node.type === 'apply'" />
                      <UploadFilled v-else-if="node.type === 'deploy'" />
                      <Bell v-else />
                    </el-icon>
                  </div>
                  <div class="pipeline-node-main">
                    <div class="pipeline-node-title">
                      {{ node.name || nodeTypeLabel(node.type) }}
                      <el-tag v-if="!node.enabled && node.type !== 'apply'" size="small" type="info" effect="plain">{{ t('tasks.disabled') }}</el-tag>
                    </div>
                    <div class="pipeline-node-summary">{{ nodeSummary(node) }}</div>
                  </div>
                  <div v-if="node.type !== 'apply'" class="pipeline-node-actions" @click.stop>
                    <el-switch v-model="node.enabled" size="small" />
                    <el-button text circle size="small" :icon="ArrowUp" :disabled="index <= 1" @click="moveNode(index, -1)" />
                    <el-button text circle size="small" :icon="ArrowDown" :disabled="index >= form.pipeline.length - 1" @click="moveNode(index, 1)" />
                    <el-button text circle size="small" :icon="Delete" @click="removeNode(index)" />
                  </div>
                </div>
              </div>
            </TransitionGroup>

            <div class="pipeline-add">
              <el-button text type="primary" :icon="Plus" @click="addPipelineNode('deploy')">{{ t('tasks.deployModule') }}</el-button>
              <el-button text :icon="Plus" @click="addPipelineNode('notify')">{{ t('tasks.notifyModule') }}</el-button>
            </div>
          </div>
        </aside>

        <section class="pipeline-config">
          <template v-if="selectedNode">
            <div class="config-head">
              <div class="config-head-title">
                <span class="config-type-dot" :class="`type-${selectedNode.type}`"></span>
                {{ nodeTypeLabel(selectedNode.type) }}
              </div>
              <el-tag size="small" effect="plain">{{ nodeStateLabel(selectedNode) }}</el-tag>
            </div>

            <el-form label-position="top" class="config-form">
              <el-form-item :label="t('tasks.moduleName')">
                <el-input v-model="selectedNode.name" :placeholder="nodeTypeLabel(selectedNode.type)" maxlength="40" />
              </el-form-item>

              <template v-if="selectedNode.type === 'apply'">
                <el-form-item :label="t('tasks.domains')" required>
                  <el-input
                    v-model="form.domainsText"
                    type="textarea"
                    :rows="3"
                    placeholder="example.com&#10;www.example.com&#10;*.example.com"
                  />
                  <div class="form-help">{{ t('tasks.domainsHelp') }}</div>
                </el-form-item>
                <el-form-item v-if="!isPrivateCA" :label="t('tasks.dnsAccount')" required>
                  <el-select v-model="form.dns_account_id" :placeholder="t('tasks.dnsPlaceholder')" style="width: 100%">
                    <el-option v-for="item in dnsAccounts" :key="item.id" :label="`${item.name} · ${dnsLabel(item.provider)}`" :value="item.id" />
                  </el-select>
                  <div v-if="!dnsAccounts.length" class="form-help warn">{{ t('tasks.noDns') }}</div>
                </el-form-item>
                <div v-else class="config-tip" style="margin-bottom: 16px">{{ t('tasks.privateCADnsTip') }}</div>
                <div class="grid grid-2" style="gap: 0 16px">
                  <el-form-item :label="t('tasks.caLabel')">
                    <el-select v-model="form.ca" style="width: 100%">
                      <el-option-group :label="t('tasks.publicCA')">
                        <el-option v-for="ca in meta?.cas || []" :key="ca.value" :label="ca.label" :value="ca.value" />
                      </el-option-group>
                      <el-option-group v-if="meta?.private_cas?.length" :label="t('tasks.privateCA')">
                        <el-option v-for="ca in meta?.private_cas || []" :key="ca.value" :label="ca.label" :value="ca.value" />
                      </el-option-group>
                    </el-select>
                  </el-form-item>
                  <el-form-item :label="t('tasks.keyType')">
                    <el-select v-model="form.key_type" style="width: 100%">
                      <el-option v-for="item in meta?.key_types || []" :key="item.value" :label="item.label" :value="item.value" />
                    </el-select>
                  </el-form-item>
                </div>
                <div class="grid grid-2" style="gap: 0 16px">
                  <el-form-item :label="t('tasks.autoRenew')">
                    <div class="flex gap-10">
                      <el-switch v-model="form.auto_renew" />
                      <span class="muted">{{ t('tasks.renewHint', { days: form.renew_days }) }}</span>
                    </div>
                  </el-form-item>
                  <el-form-item :label="t('tasks.renewDays')">
                    <el-input-number v-model="form.renew_days" :min="1" :max="60" style="width: 100%" />
                  </el-form-item>
                </div>
                <div class="config-tip">{{ isPrivateCA ? t('tasks.privateConfigTip') : t('tasks.configTip') }}</div>
              </template>

              <template v-else-if="selectedNode.type === 'deploy'">
                <el-form-item :label="t('tasks.deployTarget')" required>
                  <el-select
                    v-model="selectedNode.config.deploy_account_id"
                    :placeholder="t('tasks.deployPlaceholder')"
                    style="width: 100%"
                    @change="onDeployAccountChange"
                  >
                    <el-option
                      v-for="item in deployAccounts"
                      :key="item.id"
                      :label="`${item.name} · ${deployLabel(item.type)}`"
                      :value="item.id"
                    />
                  </el-select>
                  <div v-if="!deployAccounts.length" class="form-help warn">
                    {{ t('tasks.noDeploy') }}
                  </div>
                </el-form-item>
                <DeployConfigFields
                  v-if="selectedDeployAccount"
                  :account-type="selectedDeployAccount.type"
                  :model-value="selectedNode.config.deploy_config || {}"
                  @update:model-value="selectedNode.config.deploy_config = $event"
                />
                <el-form-item :label="t('tasks.moduleStatus')">
                  <div class="flex gap-10">
                    <el-switch v-model="selectedNode.enabled" />
                    <span class="muted">{{ selectedNode.enabled ? t('tasks.deployEnabledHint') : t('tasks.deployDisabledHint') }}</span>
                  </div>
                </el-form-item>
                <div class="config-tip">{{ t('tasks.deployTip') }}</div>
              </template>

              <template v-else-if="selectedNode.type === 'notify'">
                <el-form-item :label="t('tasks.notifyChannel')">
                  <el-select
                    v-model="selectedNode.config.notify_channels"
                    multiple
                    collapse-tags
                    collapse-tags-tooltip
                    clearable
                    style="width: 100%"
                    :placeholder="t('tasks.notifyChannelPlaceholder')"
                  >
                    <el-option
                      v-for="item in availableNotifyChannels"
                      :key="item.value"
                      :label="t(item.labelKey)"
                      :value="item.value"
                    />
                  </el-select>
                  <div class="form-help">
                    {{ availableNotifyChannels.length ? t('tasks.notifyChannelHelp') : t('tasks.notifyChannelEmpty') }}
                  </div>
                </el-form-item>
                <el-form-item :label="t('tasks.trigger')">
                  <el-radio-group v-model="selectedNode.config.notify_on">
                    <el-radio-button value="always">{{ t('tasks.always') }}</el-radio-button>
                    <el-radio-button value="success">{{ t('tasks.successOnly') }}</el-radio-button>
                    <el-radio-button value="failure">{{ t('tasks.failureOnly') }}</el-radio-button>
                  </el-radio-group>
                  <div class="form-help">{{ t('tasks.triggerHelp') }}</div>
                </el-form-item>
                <el-form-item :label="t('tasks.subject')">
                  <el-input v-model="selectedNode.config.subject" :placeholder="t('tasks.subjectPlaceholder')" maxlength="80" show-word-limit />
                </el-form-item>
                <el-form-item :label="t('tasks.moduleStatus')">
                  <div class="flex gap-10">
                    <el-switch v-model="selectedNode.enabled" />
                    <span class="muted">{{ selectedNode.enabled ? t('tasks.notifyEnabledHint') : t('tasks.notifyDisabledHint') }}</span>
                  </div>
                </el-form-item>
              </template>

              <div v-if="selectedNode.type !== 'apply'" class="config-danger">
                <el-button text type="danger" :icon="Delete" @click="removeSelectedNode">{{ t('tasks.removeModule') }}</el-button>
              </div>
            </el-form>
          </template>
          <el-empty v-else :description="t('tasks.selectNode')" :image-size="90" />
        </section>
      </div>

      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('tasks.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ editingId ? t('tasks.saveChanges') : t('tasks.createAndRun') }}</el-button>
      </template>
    </el-dialog>

    <!-- 任务日志 -->
    <el-drawer v-model="logVisible" :title="t('tasks.logTitle', { name: currentTask?.name || '' })" size="560px">
      <div v-loading="logLoading">
        <div v-if="!logs.length" class="muted" style="text-align: center; padding: 40px 0">{{ t('tasks.noLogs') }}</div>
        <div v-for="log in logs" :key="log.id" class="log-row">
          <div class="log-dot" :style="{ background: logColor(log.level) }"></div>
          <div class="log-body">
            <div class="log-msg">{{ log.message }}</div>
            <div class="log-time">{{ formatDate(log.created_at) }} · {{ log.source }}</div>
          </div>
          <el-tag :type="levelType(log.level)" size="small" effect="plain">{{ log.level }}</el-tag>
        </div>
      </div>
      <template #footer>
        <el-button @click="refreshLogs" :loading="logLoading">{{ t('tasks.refreshLogs') }}</el-button>
      </template>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, ArrowUp, Close, Delete, Plus, Refresh, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import { api } from '../api'
import { withSecondaryVerify } from '../secondaryVerify'
import { loadMeta, loadSettings } from '../store'
import { enabledNotifyChannels } from '../notifyChannels'
import DeployConfigFields from '../components/DeployConfigFields.vue'
import {
  formatDate,
  fromNow,
  levelType,
  shortDomains,
  splitLines,
  taskState
} from '../utils'
import type {
  DNSAccount,
  DeployAccount,
  LogEntry,
  MessageResult,
  Meta,
  PipelineNode,
  PipelineNodeType,
  Settings,
  Task,
  TaskBatchResult
} from '../types'

interface TaskForm {
  name: string
  domainsText: string
  dns_account_id: number | null
  ca: string
  key_type: string
  auto_renew: boolean
  renew_days: number
  enabled: boolean
  pipeline: PipelineNode[]
}

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const tasks = ref<Task[]>([])
const tableRef = ref<{ clearSelection: () => void } | null>(null)
const selectedTasks = ref<Task[]>([])
const batchRunning = ref(false)
const batchToggling = ref(false)
const dnsAccounts = ref<DNSAccount[]>([])
const deployAccounts = ref<DeployAccount[]>([])
const meta = ref<Meta | null>(null)
const notifySettings = ref<Settings | null>(null)
const availableNotifyChannels = computed(() => enabledNotifyChannels(notifySettings.value))
const dialogVisible = ref(false)
const editingId = ref(0)
const selectedNodeId = ref('apply')
const pipelineListRef = ref<HTMLElement | null>(null)
const draggingId = ref('')
const itemShifts = ref<Record<string, number>>({})
const settling = ref(false)

const logVisible = ref(false)
const logLoading = ref(false)
const logs = ref<LogEntry[]>([])
const currentTask = ref<Task | null>(null)
let logTimer: ReturnType<typeof setInterval> | null = null
let nodeSeq = 0

const form = reactive<TaskForm>({
  name: '',
  domainsText: '',
  dns_account_id: null,
  ca: 'letsencrypt',
  key_type: 'ecdsa',
  auto_renew: true,
  renew_days: 30,
  enabled: true,
  pipeline: []
})

const selectedNode = computed(() => form.pipeline.find((node) => node.id === selectedNodeId.value) || null)
const selectedDeployAccount = computed(() =>
  deployAccounts.value.find((item) => item.id === selectedNode.value?.config.deploy_account_id) || null
)
const isPrivateCA = computed(() => form.ca.startsWith('private-ca:'))

function caLabel(value?: string): string {
  if (!value) return ''
  const publicCA = (meta.value?.cas || []).find((item) => item.value === value)
  if (publicCA) return publicCA.label
  return (meta.value?.private_cas || []).find((item) => item.value === value)?.label || value
}
function dnsLabel(value?: string): string {
  return (meta.value?.dns_providers || []).find((item) => item.value === value)?.label || value || ''
}
function deployLabel(value?: string): string {
  return (meta.value?.deploy_types || []).find((item) => item.value === value)?.label || value || ''
}
function deployTypeDef(type?: string) {
  return (meta.value?.deploy_types || []).find((item) => item.value === type)
}
function defaultDeployConfig(type?: string): Record<string, string> {
  const config: Record<string, string> = {}
  for (const field of deployTypeDef(type)?.deploy_fields || []) {
    if (field.default) config[field.key] = field.default
    else if (field.type === 'select' && field.options?.length) config[field.key] = field.options[0].value
    else config[field.key] = ''
  }
  return config
}
function normalizeDeployConfig(type: string | undefined, current: Record<string, string> | undefined): Record<string, string> {
  const def = deployTypeDef(type)
  // 插件未安装时元数据不在列表中，保留原参数，避免编辑任务时误清空。
  if (!def) return { ...(current || {}) }
  const fields = def.deploy_fields || []
  const allowed = new Set(fields.map((field) => field.key))
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(current || {})) {
    if (allowed.has(key)) out[key] = value
  }
  for (const [key, value] of Object.entries(defaultDeployConfig(type))) {
    if (out[key] === undefined) out[key] = value
  }
  return out
}
function onDeployAccountChange(): void {
  if (!selectedNode.value) return
  selectedNode.value.config.deploy_config = normalizeDeployConfig(
    selectedDeployAccount.value?.type,
    selectedNode.value.config.deploy_config
  )
}
function nodeTypeLabel(type: PipelineNodeType): string {
  if (type === 'apply') return t('tasks.pipelineApply')
  if (type === 'deploy') return t('tasks.pipelineDeploy')
  return t('tasks.pipelineNotify')
}
function nodeStateLabel(node: PipelineNode): string {
  if (node.type === 'apply') return t('tasks.applyModule')
  return node.enabled ? t('tasks.moduleEnabled') : t('tasks.moduleDisabled')
}
function uid(prefix: string): string {
  nodeSeq += 1
  return `${prefix}-${Date.now()}-${nodeSeq}`
}
function newNode(type: PipelineNodeType): PipelineNode {
  if (type === 'deploy') {
    const account = deployAccounts.value[0]
    return {
      id: uid('deploy'),
      type,
      name: '',
      enabled: true,
      config: {
        deploy_account_id: account?.id || 0,
        deploy_config: normalizeDeployConfig(account?.type, {})
      }
    }
  }
  return {
    id: uid('notify'),
    type: 'notify',
    name: '',
    enabled: true,
    config: { notify_channel: 'all', notify_channels: [], notify_on: 'always', subject: '' }
  }
}
function defaultPipeline(): PipelineNode[] {
  return [
    { id: 'apply', type: 'apply', name: '', enabled: true, config: {} },
    newNode('notify')
  ]
}

function clonePipeline(nodes: PipelineNode[]): PipelineNode[] {
  return nodes.map((node) => ({
    ...node,
    config: { ...node.config }
  }))
}

function taskPipeline(row: Task): PipelineNode[] {
  if (row.pipeline?.length) return row.pipeline
  const nodes: PipelineNode[] = [
    {
      id: 'apply',
      type: 'apply',
      name: '',
      enabled: true,
      config: {
        domains: row.domains,
        dns_account_id: row.dns_account_id,
        ca: row.ca,
        key_type: row.key_type,
        auto_renew: row.auto_renew,
        renew_days: row.renew_days
      }
    }
  ]
  ;(row.deploy_ids || []).forEach((id, index) => {
    const account = deployAccounts.value.find((item) => item.id === id)
    nodes.push({
      id: `deploy-${index + 1}`,
      type: 'deploy',
      name: '',
      enabled: true,
      config: {
        deploy_account_id: id,
        deploy_config: normalizeDeployConfig(account?.type, account?.config)
      }
    })
  })
  nodes.push({
    id: 'notify',
    type: 'notify',
    name: '',
    enabled: true,
    config: { notify_channel: 'all', notify_on: 'always' }
  })
  return nodes
}

function pipelineSummary(row: Task): string {
  const nodes = taskPipeline(row)
  const deployCount = nodes.filter((node) => node.type === 'deploy' && node.enabled).length
  const notifyCount = nodes.filter((node) => node.type === 'notify' && node.enabled).length
  const parts = [t('tasks.summaryApply')]
  if (deployCount) parts.push(t('tasks.summaryDeploy', { count: deployCount }))
  if (notifyCount) parts.push(t('tasks.summaryNotify'))
  return parts.join(' → ')
}

function nodeSummary(node: PipelineNode): string {
  if (node.type === 'apply') {
    const domains = splitLines(form.domainsText)
    return `${domains.length ? shortDomains(domains, 2) : t('tasks.notSetDomains')} · ${caLabel(form.ca)}`
  }
  if (node.type === 'deploy') {
    const account = deployAccounts.value.find((item) => item.id === node.config.deploy_account_id)
    if (!account) return t('tasks.notSelected')
    const path = node.config.deploy_config?.cert_path || node.config.deploy_config?.key_path || node.config.deploy_config?.domains || ''
    return path ? `${account.name} · ${path}` : `${account.name} · ${deployLabel(account.type)}`
  }
  const when = node.config.notify_on === 'success' ? t('tasks.successOnly') : node.config.notify_on === 'failure' ? t('tasks.failureOnly') : t('tasks.always')
  return `${t('tasks.email')} · ${when}`
}

async function loadAll(): Promise<void> {
  loading.value = true
  try {
    const [taskList, dnsList, deployList, metaData] = await Promise.all([
      api.get<Task[]>('/tasks'),
      api.get<DNSAccount[]>('/dns-accounts'),
      api.get<DeployAccount[]>('/deploy-accounts'),
      loadMeta(true)
    ])
    tasks.value = taskList || []
    dnsAccounts.value = dnsList || []
    deployAccounts.value = deployList || []
    meta.value = metaData
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

async function openCreate(): Promise<void> {
  editingId.value = 0
  const settings = await loadSettings(true).catch(() => null)
  notifySettings.value = settings
  const defaultCA = dnsAccounts.value.length
    ? settings?.acme_ca || 'letsencrypt'
    : meta.value?.private_cas?.[0]?.value || settings?.acme_ca || 'letsencrypt'
  Object.assign(form, {
    name: '',
    domainsText: '',
    dns_account_id: dnsAccounts.value[0]?.id || null,
    ca: defaultCA,
    key_type: settings?.acme_key_type || 'ecdsa',
    auto_renew: true,
    renew_days: settings?.acme_renew_days || 30,
    enabled: true,
    pipeline: defaultPipeline()
  })
  selectedNodeId.value = 'apply'
  dialogVisible.value = true
}

async function openEdit(row: Task): Promise<void> {
  notifySettings.value = await loadSettings(true).catch(() => null)
  editingId.value = row.id
  const pipeline = clonePipeline(taskPipeline(row))
  for (const node of pipeline) {
    if (node.type === 'notify' && !Array.isArray(node.config.notify_channels)) {
      node.config.notify_channels = []
    }
    if (node.type === 'deploy') {
      const account = deployAccounts.value.find((item) => item.id === node.config.deploy_account_id)
      node.config.deploy_config = normalizeDeployConfig(
        account?.type,
        node.config.deploy_config || account?.config
      )
    }
  }
  Object.assign(form, {
    name: row.name,
    domainsText: (row.domains || []).join('\n'),
    dns_account_id: row.dns_account_id || null,
    ca: row.ca,
    key_type: row.key_type,
    auto_renew: row.auto_renew,
    renew_days: row.renew_days,
    enabled: row.enabled,
    pipeline
  })
  selectedNodeId.value = 'apply'
  dialogVisible.value = true
}

function selectNode(node: PipelineNode): void {
  if (suppressClick) return
  selectedNodeId.value = node.id
}

function addPipelineNode(type: PipelineNodeType): void {
  if (type === 'apply') return
  const node = newNode(type)
  form.pipeline.push(node)
  selectedNodeId.value = node.id
}

function moveNode(index: number, delta: number): void {
  const target = index + delta
  if (target < 1 || target >= form.pipeline.length) return
  const [node] = form.pipeline.splice(index, 1)
  form.pipeline.splice(target, 0, node)
}

// 拖动排序使用指针事件而非原生 HTML5 DnD：原生拖拽只能拿到粒度较粗的 drag 事件，
// 无法在拖动过程中做跟随动画。这里用一张跟随指针的 ghost 卡片，同时把列表里的
// 卡片实时平移到目标位置，形成“其他模块让位”的动画；松手后再提交数组顺序。
interface DragMetric {
  id: string
  top: number
  height: number
  cardTop: number
  cardBottom: number
}

let dragFrom = -1
let dragNodeId = ''
let dragCardEl: HTMLElement | null = null
let dragGhostEl: HTMLElement | null = null
let dragTrackEl: HTMLElement | null = null
let dragMetrics: DragMetric[] = []
let dragInsertIndex = -1
let dragGhostHeight = 0
let dragPointerId = -1
let dragStartX = 0
let dragStartY = 0
let dragLastX = 0
let dragLastY = 0
let dragGrabX = 0
let dragGrabY = 0
let dragRaf = 0
let dragActive = false
let suppressClick = false

function itemShiftStyle(id: string): Record<string, string> | undefined {
  const shift = itemShifts.value[id]
  if (!shift) return undefined
  return { transform: `translateY(${shift}px)` }
}

function onHandlePointerDown(event: PointerEvent, index: number, node: PipelineNode): void {
  if (index < 1) return
  if (event.pointerType === 'mouse' && event.button !== 0) return
  const handle = event.currentTarget as HTMLElement | null
  const card = handle?.closest<HTMLElement>('.pipeline-node') || null
  if (!handle || !card) return
  // 在按下时选中模块，避免松手提交后再切换右侧配置面板造成一帧卡顿。
  selectedNodeId.value = node.id
  try {
    handle.setPointerCapture(event.pointerId)
  } catch {
    // 某些浏览器/输入设备不支持指针捕获，window 上的监听仍然可以兜底。
  }
  const rect = card.getBoundingClientRect()
  dragFrom = index
  dragNodeId = node.id
  dragCardEl = card
  dragPointerId = event.pointerId
  dragStartX = dragLastX = event.clientX
  dragStartY = dragLastY = event.clientY
  dragGrabX = event.clientX - rect.left
  dragGrabY = event.clientY - rect.top
  dragActive = false
  dragInsertIndex = -1
  window.addEventListener('pointermove', onPointerMove)
  window.addEventListener('pointerup', onPointerUp)
  window.addEventListener('pointercancel', onPointerCancel)
}

function onPointerMove(event: PointerEvent): void {
  if (event.pointerId !== dragPointerId) return
  dragLastX = event.clientX
  dragLastY = event.clientY
  if (dragActive) return
  if (Math.hypot(event.clientX - dragStartX, event.clientY - dragStartY) < 4) return
  startDrag()
}

function startDrag(): void {
  if (!dragCardEl) return
  dragActive = true
  draggingId.value = dragNodeId
  document.body.classList.add('pipeline-dragging')

  // 记录拖动开始时的布局位置。getBoundingClientRect 能保留小数高度，
  // 避免用 offsetHeight 取整后多张卡片累计出可见的位置偏差。
  itemShifts.value = {}
  dragTrackEl = pipelineListRef.value?.querySelector<HTMLElement>('.pipeline-track') || null
  if (dragTrackEl) {
    const trackRect = dragTrackEl.getBoundingClientRect()
    dragMetrics = Array.from(dragTrackEl.querySelectorAll<HTMLElement>('.pipeline-item')).map((el, index) => {
      const rect = el.getBoundingClientRect()
      const card = el.querySelector<HTMLElement>('.pipeline-node')
      const cardRect = card ? card.getBoundingClientRect() : rect
      return {
        id: form.pipeline[index]?.id || '',
        top: rect.top - trackRect.top,
        height: rect.height,
        cardTop: cardRect.top - trackRect.top,
        cardBottom: cardRect.bottom - trackRect.top
      }
    })
  } else {
    dragMetrics = []
  }

  const rect = dragCardEl.getBoundingClientRect()
  dragGhostHeight = rect.height
  const ghost = dragCardEl.cloneNode(true) as HTMLElement
  ghost.classList.add('pipeline-node--ghost')
  ghost.setAttribute('aria-hidden', 'true')
  ghost.querySelectorAll('[id]').forEach((el) => el.removeAttribute('id'))
  ghost.style.width = `${rect.width}px`
  ghost.style.height = `${rect.height}px`
  ghost.style.transform = `translate3d(${rect.left}px, ${rect.top}px, 0)`
  document.body.appendChild(ghost)
  dragGhostEl = ghost

  updateDropPreview(dragLastY)
  dragRaf = requestAnimationFrame(dragFrame)
}

function dragFrame(): void {
  if (!dragActive) return
  autoScrollList()
  updateDropPreview(dragLastY)
  moveGhost()
  dragRaf = requestAnimationFrame(dragFrame)
}

// 指针靠近列表上下边缘时自动滚动，避免模块较多时无法拖到可视区之外。
function autoScrollList(): void {
  const list = pipelineListRef.value
  if (!list) return
  const rect = list.getBoundingClientRect()
  if (dragLastX < rect.left - 40 || dragLastX > rect.right + 40) return
  if (dragLastY < rect.top - 40 || dragLastY > rect.bottom + 40) return
  const edge = 36
  let delta = 0
  if (dragLastY < rect.top + edge) {
    const ratio = Math.min((rect.top + edge - dragLastY) / edge, 1)
    delta = -Math.ceil(ratio * 12)
  } else if (dragLastY > rect.bottom - edge) {
    const ratio = Math.min((dragLastY - (rect.bottom - edge)) / edge, 1)
    delta = Math.ceil(ratio * 12)
  }
  if (!delta) return
  const max = list.scrollHeight - list.clientHeight
  const next = Math.min(Math.max(list.scrollTop + delta, 0), max)
  if (next !== list.scrollTop) list.scrollTop = next
}

// 以拖动卡片的前缘作为插入光标：
// 向上拖时，上边缘碰到上方卡片的下边缘就触发让位；
// 向下拖时，下边缘越过下方卡片的下边缘才触发让位。
function updateDropPreview(pointerY: number): void {
  const track = dragTrackEl
  if (!track || dragMetrics.length === 0 || dragFrom < 1) return

  const from = dragFrom
  const own = dragMetrics[from]
  if (!own) return

  const trackTop = track.getBoundingClientRect().top
  const ghostTop = pointerY - dragGrabY - trackTop
  const ghostBottom = ghostTop + dragGhostHeight

  let insertAt = from
  if (ghostTop <= own.cardTop) {
    // 向上移动：上边缘碰到上方卡片的下边缘时，插入到它前面，该卡片下移。
    for (let i = 1; i < dragMetrics.length; i += 1) {
      if (ghostTop <= dragMetrics[i].cardBottom) {
        insertAt = i
        break
      }
    }
  } else {
    // 向下移动：下边缘越过某张卡片的下边缘时，插入到它后面，该卡片上移。
    for (let i = from; i < dragMetrics.length; i += 1) {
      if (ghostBottom > dragMetrics[i].cardBottom) {
        insertAt = i + 1
      } else {
        break
      }
    }
  }

  // “申请证书”节点固定在首位，只能把其他模块插入到它后面。
  insertAt = Math.max(1, Math.min(insertAt, dragMetrics.length))
  if (insertAt === dragInsertIndex) return
  dragInsertIndex = insertAt

  const targetIndex = insertAt > from ? insertAt - 1 : insertAt
  if (targetIndex === from) {
    itemShifts.value = {}
    return
  }

  const ids = dragMetrics.map((metric) => metric.id)
  const finalIds = ids.filter((_, index) => index !== from)
  finalIds.splice(targetIndex, 0, ids[from])
  const metricById = new Map(dragMetrics.map((metric) => [metric.id, metric]))

  let top = 0
  const shifts: Record<string, number> = {}
  for (const id of finalIds) {
    const metric = metricById.get(id)
    if (!metric) continue
    const shift = top - metric.top
    if (shift) shifts[id] = shift
    top += metric.height
  }
  itemShifts.value = shifts
}

function moveGhost(): void {
  if (!dragGhostEl) return
  const x = dragLastX - dragGrabX
  const y = dragLastY - dragGrabY
  dragGhostEl.style.transform = `translate3d(${x}px, ${y}px, 0) scale(1.02)`
}

function onPointerUp(event: PointerEvent): void {
  if (event.pointerId !== dragPointerId) return
  if (dragActive) {
    dragLastX = event.clientX
    dragLastY = event.clientY
    updateDropPreview(dragLastY)
  }
  const from = dragFrom
  const insertAt = dragInsertIndex
  const wasActive = dragActive
  if (!wasActive) {
    stopDrag()
    return
  }
  // 拖动结束后浏览器仍可能补发一次 click，避免误切换右侧配置。
  suppressClick = true
  window.setTimeout(() => {
    suppressClick = false
  }, 60)
  if (from < 1 || insertAt < 1) {
    stopDrag()
    return
  }
  const target = insertAt > from ? insertAt - 1 : insertAt
  if (target === from) {
    stopDrag()
    return
  }
  // 提交顺序的这一帧关闭 transform 过渡。拖动过程中卡片已经被预览平移到目标位置，
  // 如果此时再让 CSS 从旧 transform 补间到新布局，卡片会先跳一下再滑回来，看起来像卡顿。
  settling.value = true
  stopDrag()
  const [node] = form.pipeline.splice(from, 1)
  form.pipeline.splice(target, 0, node)
  selectedNodeId.value = node.id
  nextTick(() => {
    settling.value = false
  })
}

function onPointerCancel(event: PointerEvent): void {
  if (event.pointerId !== dragPointerId) return
  stopDrag()
}

function stopDrag(): void {
  if (dragRaf) {
    cancelAnimationFrame(dragRaf)
    dragRaf = 0
  }
  dragGhostEl?.remove()
  dragGhostEl = null
  dragCardEl = null
  dragTrackEl = null
  dragMetrics = []
  dragInsertIndex = -1
  dragGhostHeight = 0
  dragActive = false
  dragFrom = -1
  dragNodeId = ''
  dragPointerId = -1
  draggingId.value = ''
  itemShifts.value = {}
  document.body.classList.remove('pipeline-dragging')
  window.removeEventListener('pointermove', onPointerMove)
  window.removeEventListener('pointerup', onPointerUp)
  window.removeEventListener('pointercancel', onPointerCancel)
}

function removeNode(index: number): void {
  if (index <= 0) return
  form.pipeline.splice(index, 1)
  if (!form.pipeline.some((node) => node.id === selectedNodeId.value)) {
    selectedNodeId.value = form.pipeline[0]?.id || ''
  }
}

function removeSelectedNode(): void {
  const index = form.pipeline.findIndex((node) => node.id === selectedNodeId.value)
  removeNode(index)
}

function buildPayload(): Record<string, unknown> {
  const domains = splitLines(form.domainsText)
  const pipeline = form.pipeline.map((node) => {
    if (node.type === 'apply') {
      return {
        ...node,
        config: {
          domains,
          dns_account_id: form.dns_account_id || 0,
          ca: form.ca,
          key_type: form.key_type,
          auto_renew: form.auto_renew,
          renew_days: form.renew_days
        }
      }
    }
    return { ...node, config: { ...node.config } }
  })
  return {
    name: form.name.trim(),
    domains,
    challenge: 'dns01',
    dns_account_id: form.dns_account_id,
    ca: form.ca,
    key_type: form.key_type,
    auto_renew: form.auto_renew,
    renew_days: form.renew_days,
    deploy_ids: pipeline
      .filter((node) => node.type === 'deploy' && node.enabled)
      .map((node) => node.config.deploy_account_id || 0)
      .filter(Boolean),
    pipeline,
    enabled: form.enabled
  }
}

async function save(): Promise<void> {
  const domains = splitLines(form.domainsText)
  if (!domains.length) {
    ElMessage.warning(t('tasks.domainsRequired'))
    selectedNodeId.value = 'apply'
    return
  }
  if (!isPrivateCA.value && !form.dns_account_id) {
    ElMessage.warning(t('tasks.dnsRequired'))
    selectedNodeId.value = 'apply'
    return
  }
  for (const node of form.pipeline) {
    if (node.type !== 'deploy' || !node.enabled) continue
    const account = deployAccounts.value.find((item) => item.id === node.config.deploy_account_id)
    if (!account) {
      ElMessage.warning(t('tasks.deployRequired'))
      selectedNodeId.value = node.id
      return
    }
    const missing = (deployTypeDef(account.type)?.deploy_fields || []).find(
      (field) => field.required && !String(node.config.deploy_config?.[field.key] || '').trim()
    )
    if (missing) {
      ElMessage.warning(t('tasks.deployConfigRequired', { field: missing.label }))
      selectedNodeId.value = node.id
      return
    }
  }
  saving.value = true
  try {
    const payload = buildPayload()
    const saved = await withSecondaryVerify('task_write', (headers) =>
      editingId.value
        ? api.put(`/tasks/${editingId.value}`, payload, headers)
        : api.post('/tasks', payload, headers)
    )
    if (saved === null) return
    ElMessage.success(t(editingId.value ? 'tasks.saved' : 'tasks.created'))
    dialogVisible.value = false
    await loadAll()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function runTask(row: Task): Promise<void> {
  try {
    await ElMessageBox.confirm(t('tasks.runConfirm', { name: row.name }), t('tasks.runTitle'), {
      confirmButtonText: t('tasks.run'),
      cancelButtonText: t('common.cancel')
    })
  } catch (_) {
    return
  }
  try {
    const data = await api.post<MessageResult>(`/tasks/${row.id}/run`)
    ElMessage.success(data.message || t('tasks.runStarted'))
    setTimeout(loadAll, 1200)
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function onSelectionChange(rows: Task[]): void {
  selectedTasks.value = rows
}

function clearSelection(): void {
  tableRef.value?.clearSelection()
  selectedTasks.value = []
}

async function batchRun(): Promise<void> {
  const ids = selectedTasks.value.map((task) => task.id)
  if (!ids.length) return
  const running = selectedTasks.value.filter((task) => task.status === 'running').length
  try {
    await ElMessageBox.confirm(
      running
        ? t('tasks.batchRunConfirmSkipped', { count: ids.length, running })
        : t('tasks.batchRunConfirm', { count: ids.length }),
      t('tasks.batchRunTitle'),
      { confirmButtonText: t('tasks.batchRun'), cancelButtonText: t('common.cancel') }
    )
  } catch (_) {
    return
  }
  batchRunning.value = true
  try {
    const data = await api.post<TaskBatchResult>('/tasks/batch/run', { ids })
    ElMessage.success(data.message || t('tasks.batchRunDone', { started: data.started || 0, skipped: data.skipped || 0 }))
    await loadAll()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    batchRunning.value = false
  }
}

async function batchSetEnabled(enabled: boolean): Promise<void> {
  const ids = selectedTasks.value.map((task) => task.id)
  if (!ids.length) return
  try {
    await ElMessageBox.confirm(
      t(enabled ? 'tasks.batchEnableConfirm' : 'tasks.batchPauseConfirm', { count: ids.length }),
      t(enabled ? 'tasks.batchEnableTitle' : 'tasks.batchPauseTitle'),
      { confirmButtonText: t(enabled ? 'tasks.batchEnable' : 'tasks.batchPause'), cancelButtonText: t('common.cancel') }
    )
  } catch (_) {
    return
  }
  batchToggling.value = true
  try {
    const data = await api.post<TaskBatchResult>('/tasks/batch/enabled', { ids, enabled })
    ElMessage.success(data.message || t('tasks.batchUpdated', { count: data.updated || 0 }))
    await loadAll()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    batchToggling.value = false
  }
}

function taskPayload(row: Task, enabled: boolean): Record<string, unknown> {
  const pipeline = clonePipeline(taskPipeline(row))
  return {
    name: row.name,
    domains: row.domains,
    challenge: 'dns01',
    dns_account_id: row.dns_account_id,
    ca: row.ca,
    key_type: row.key_type,
    auto_renew: row.auto_renew,
    renew_days: row.renew_days,
    deploy_ids: row.deploy_ids,
    pipeline,
    enabled
  }
}

async function handleMore(command: string, row: Task): Promise<void> {
  if (command === 'edit') {
    await openEdit(row)
    return
  }
  if (command === 'delete') {
    try {
      await ElMessageBox.confirm(t('tasks.deleteConfirm', { name: row.name }), t('tasks.deleteTitle'), {
        confirmButtonText: t('common.delete'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      })
    } catch (_) {
      return
    }
    try {
      const deleted = await withSecondaryVerify('task_write', (headers) => api.delete(`/tasks/${row.id}`, headers))
      if (deleted === null) return
      ElMessage.success(t('tasks.deleted'))
      await loadAll()
      clearSelection()
    } catch (error: any) {
      ElMessage.error(error.message)
    }
    return
  }
  if (command === 'toggle') {
    try {
      const saved = await withSecondaryVerify('task_write', (headers) =>
        api.put(`/tasks/${row.id}`, taskPayload(row, !row.enabled), headers)
      )
      if (saved === null) return
      ElMessage.success(!row.enabled ? t('tasks.enabledMsg') : t('tasks.pausedMsg'))
      loadAll()
    } catch (error: any) {
      ElMessage.error(error.message)
    }
  }
}

async function openLogs(row: Task): Promise<void> {
  currentTask.value = row
  logVisible.value = true
  await refreshLogs()
}

async function refreshLogs(): Promise<void> {
  if (!currentTask.value) return
  logLoading.value = true
  try {
    logs.value = (await api.get<LogEntry[]>(`/tasks/${currentTask.value.id}/logs`)) || []
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    logLoading.value = false
  }
}

function logColor(level: string): string {
  const map: Record<string, string> = { success: '#10b981', warning: '#f59e0b', error: '#ef4444' }
  return map[level] || '#3b82f6'
}

onMounted(() => {
  loadAll()
  logTimer = setInterval(() => {
    if (logVisible.value) refreshLogs()
  }, 5000)
})
onUnmounted(() => {
  if (logTimer) clearInterval(logTimer)
  stopDrag()
})
</script>

<style scoped>
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
.batch-count {
  font-size: 13px;
  font-weight: 600;
  color: #1d4ed8;
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
/* 表格自带的底边和最后一行边线会与卡片底边形成双线，这里清除 */
.task-list :deep(.el-table__inner-wrapper::before) {
  display: none;
}
.task-list :deep(.el-table__body tr:last-child > td.el-table__cell) {
  border-bottom: 0;
}
/* 操作列按钮保持单行，避免换行导致行高不一致 */
.task-list :deep(.el-table__cell:last-child .cell) {
  white-space: nowrap;
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  line-height: 1.6;
  margin-top: 6px;
}
.form-help.warn {
  color: #d97706;
}
.pipeline-summary {
  min-width: 0;
  overflow: hidden;
}
.pipeline-summary-text {
  display: inline-block;
  max-width: 100%;
  font-size: 12px;
  color: #475569;
  background: #f4f7fc;
  border: 1px solid #e8eef7;
  border-radius: 8px;
  padding: 4px 8px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: middle;
}
.dialog-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 18px;
  padding-bottom: 16px;
  border-bottom: 1px solid #eef2f8;
}
.dialog-head-main {
  flex: 1;
  min-width: 0;
}
.dialog-head-main .el-input {
  max-width: 520px;
}
.dialog-hint {
  display: block;
  margin-top: 8px;
  font-size: 12px;
  color: #94a3b8;
}
.dialog-enable {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
  padding-top: 6px;
}
.pipeline-editor {
  display: grid;
  grid-template-columns: 390px minmax(0, 1fr);
  gap: 18px;
  margin-top: 18px;
  min-height: 480px;
}
.pipeline-flow {
  background: #f8fafd;
  border: 1px solid #e8eef7;
  border-radius: 14px;
  padding: 14px;
  display: flex;
  flex-direction: column;
  max-height: 560px;
}
.pipeline-flow-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 2px 4px 14px;
}
.pipeline-flow-title {
  font-size: 14px;
  font-weight: 600;
  color: #0f172a;
}
.pipeline-flow-sub {
  margin-top: 3px;
  font-size: 11px;
  color: #94a3b8;
}
.pipeline-list {
  overflow-y: auto;
  padding: 2px 4px 6px;
  overscroll-behavior: contain;
}
.pipeline-track {
  position: relative;
}
.pipeline-item {
  position: relative;
  /* 拖动过程中卡片会实时平移到目标位置，这里负责“让位”的补间动画。 */
  transition: transform 0.18s cubic-bezier(0.22, 0.8, 0.24, 1), opacity 0.18s ease;
}
.pipeline-track.is-dragging .pipeline-item {
  will-change: transform;
}
/* 松手提交顺序的这一帧直接落位，避免和 TransitionGroup 的 FLIP 叠加产生回弹。 */
.pipeline-track.is-settling .pipeline-item {
  transition: none !important;
}
/* 被拖走的卡片在目标空位显示为虚线占位，内容由跟随指针的 ghost 展示。 */
.pipeline-item.is-dragging {
  pointer-events: none;
}
.pipeline-item.is-dragging .pipeline-node {
  background: #f7faff;
  border: 1px dashed #b3c9f2;
  box-shadow: none;
}
.pipeline-item.is-dragging .pipeline-node > * {
  opacity: 0;
}
.pipeline-item.is-dragging .pipeline-connector {
  opacity: 0.4;
}
.pipeline-node {
  position: relative;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 12px;
  background: #fff;
  border: 1px solid #e6ecf5;
  border-radius: 12px;
  cursor: pointer;
  transition: border-color 0.15s ease, box-shadow 0.15s ease, opacity 0.15s ease, background 0.15s ease;
}
.pipeline-node:hover {
  border-color: #bcd0f7;
}
.pipeline-node.active {
  border-color: #2563eb;
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.1);
}
.pipeline-node.muted {
  opacity: 0.68;
}
/* 箭头移动、删除节点时由 TransitionGroup 播放 FLIP 位移动画。 */
.pipeline-move,
.pipeline-enter-active,
.pipeline-leave-active {
  transition: transform 0.18s cubic-bezier(0.22, 0.8, 0.24, 1), opacity 0.18s ease;
}
.pipeline-enter-from,
.pipeline-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}
.pipeline-leave-active {
  position: absolute;
  left: 0;
  right: 0;
}
/* 跟随指针的拖动副本。 */
.pipeline-node--ghost {
  position: fixed;
  top: 0;
  left: 0;
  z-index: 3000;
  box-sizing: border-box;
  margin: 0;
  opacity: 1 !important;
  background: #fff;
  border-color: #bcd0f7;
  box-shadow: 0 18px 38px rgba(15, 23, 42, 0.2), 0 4px 10px rgba(15, 23, 42, 0.08);
  pointer-events: none;
  cursor: grabbing;
  transition: none !important;
  will-change: transform;
  transform-origin: center;
}
:global(body.pipeline-dragging),
:global(body.pipeline-dragging *) {
  cursor: grabbing !important;
  user-select: none;
}
.drag-handle {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 30px;
  margin-left: -4px;
  color: #c3cede;
  cursor: grab;
  border-radius: 6px;
  touch-action: none;
  user-select: none;
}
.drag-handle:hover {
  color: #2563eb;
  background: #eef4ff;
}
.drag-handle:active {
  cursor: grabbing;
}
.pipeline-node-icon {
  width: 34px;
  height: 34px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  font-size: 17px;
}
.pipeline-node-icon.type-apply {
  background: #eef4ff;
  color: #2563eb;
}
.pipeline-node-icon.type-deploy {
  background: #ecfdf5;
  color: #059669;
}
.pipeline-node-icon.type-notify {
  background: #fff7ed;
  color: #ea580c;
}
.pipeline-node-main {
  flex: 1;
  min-width: 0;
}
.pipeline-node-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: #1e293b;
}
.pipeline-node-summary {
  margin-top: 4px;
  font-size: 11px;
  color: #94a3b8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pipeline-node-actions {
  display: flex;
  align-items: center;
  gap: 0;
  flex: none;
}
.pipeline-node-actions .el-switch {
  margin-right: 4px;
}
.pipeline-node-actions :deep(.el-button) {
  margin-left: 0;
  padding: 5px;
  min-height: 22px;
}
.pipeline-connector {
  height: 22px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
}
.pipeline-connector-line {
  width: 1px;
  height: 12px;
  background: #d8e2f1;
}
.pipeline-connector-dot {
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: #c3d2e8;
}
.pipeline-add {
  display: flex;
  justify-content: center;
  gap: 6px;
  padding-top: 14px;
}
.pipeline-config {
  border: 1px solid #e8eef7;
  border-radius: 14px;
  padding: 18px 20px;
  min-height: 480px;
  max-height: 560px;
  overflow-y: auto;
}
.config-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}
.config-head-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
  color: #0f172a;
}
.config-type-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
}
.config-type-dot.type-apply {
  background: #2563eb;
}
.config-type-dot.type-deploy {
  background: #059669;
}
.config-type-dot.type-notify {
  background: #ea580c;
}
.config-form :deep(.el-form-item) {
  margin-bottom: 18px;
}
.config-tip {
  margin-top: 4px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #f7f9fd;
  color: #64748b;
  font-size: 12px;
  line-height: 1.7;
}
.config-danger {
  margin-top: 8px;
  padding-top: 10px;
  border-top: 1px dashed #eef2f8;
}
@media (max-width: 900px) {
  .pipeline-editor {
    grid-template-columns: 1fr;
    min-height: 0;
  }
  .pipeline-flow,
  .pipeline-config {
    max-height: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  .pipeline-move,
  .pipeline-enter-active,
  .pipeline-leave-active,
  .pipeline-item {
    transition-duration: 0.01ms !important;
  }
  .pipeline-node--ghost {
    transition: none !important;
  }
}
.log-row {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 12px 0;
  border-bottom: 1px dashed #eef1f6;
}
.log-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-top: 5px;
  flex: none;
}
.log-body {
  flex: 1;
  min-width: 0;
}
.log-msg {
  font-size: 13px;
  line-height: 1.7;
  word-break: break-word;
}
.log-time {
  font-size: 11px;
  color: #94a3b8;
  margin-top: 4px;
}
</style>
