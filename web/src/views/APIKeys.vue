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
          <el-table-column width="120" :label="t('apiKeys.ipRestriction')">
            <template #default="{ row }">
              <el-tooltip
                v-if="row.allowed_ips?.length"
                :content="row.allowed_ips.join('、')"
                placement="top"
              >
                <el-tag size="small" type="warning" effect="plain">
                  {{ t('apiKeys.ipCount', { count: row.allowed_ips.length }) }}
                </el-tag>
              </el-tooltip>
              <span v-else class="muted">{{ t('apiKeys.ipUnrestricted') }}</span>
            </template>
          </el-table-column>
          <el-table-column width="130" :label="t('apiKeys.dnsRestriction')">
            <template #default="{ row }">
              <el-tooltip
                v-if="row.allowed_dns_account_ids?.length"
                :content="row.allowed_dns_account_ids.map(dnsAccountLabel).join('、')"
                placement="top"
              >
                <el-tag size="small" type="warning" effect="plain">
                  {{ t('apiKeys.dnsCount', { count: row.allowed_dns_account_ids.length }) }}
                </el-tag>
              </el-tooltip>
              <span v-else class="muted">{{ t('apiKeys.dnsUnrestricted') }}</span>
            </template>
          </el-table-column>
          <el-table-column width="80" :label="t('common.status')">
            <template #default="{ row }">
              <span class="tag-soft" :class="statusOf(row).className">{{ statusOf(row).label }}</span>
            </template>
          </el-table-column>
          <el-table-column width="150" :label="t('apiKeys.lastUsed')">
            <template #default="{ row }">
              <template v-if="row.last_used_at">
                <div>{{ fromNow(row.last_used_at) }}</div>
                <div v-if="row.last_used_ip" class="key-prefix mono" :title="row.last_used_ip">
                  {{ row.last_used_ip }}
                </div>
              </template>
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
          <el-table-column width="280" align="right" :label="t('common.actions')">
            <template #default="{ row }">
              <el-button text type="primary" size="small" @click="openEdit(row)">{{ t('common.edit') }}</el-button>
              <el-button text size="small" @click="rotate(row)">{{ t('apiKeys.rotate') }}</el-button>
              <el-button text size="small" @click="openLogs(row)">{{ t('apiKeys.logs') }}</el-button>
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
      class="api-key-dialog"
      width="min(1040px, 94vw)"
      top="4vh"
      :close-on-click-modal="false"
      @closed="resetWebrtc"
    >
      <el-form label-position="top">
        <div class="form-row">
          <el-form-item :label="t('apiKeys.nameLabel')" required class="form-row-name">
            <el-input v-model="form.name" maxlength="64" :placeholder="t('apiKeys.namePlaceholder')" />
          </el-form-item>
          <el-form-item class="form-row-expires">
            <template #label>
              <span class="label-with-help">
                {{ t('apiKeys.expiresLabel') }}
                <el-tooltip :content="t('apiKeys.expiresHelp')" placement="top">
                  <el-icon class="label-help-icon"><InfoFilled /></el-icon>
                </el-tooltip>
              </span>
            </template>
            <el-date-picker
              v-model="form.expiresAt"
              type="datetime"
              :placeholder="t('apiKeys.expiresPlaceholder')"
              :disabled-date="disabledDate"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item v-if="editing" class="form-row-enabled">
            <template #label>
              <span class="label-with-help">
                {{ t('apiKeys.enabledLabel') }}
                <el-tooltip :content="t('apiKeys.enabledHelp')" placement="top">
                  <el-icon class="label-help-icon"><InfoFilled /></el-icon>
                </el-tooltip>
              </span>
            </template>
            <el-switch v-model="form.enabled" />
          </el-form-item>
        </div>
        <div class="form-row form-row-access">
          <el-form-item class="access-item">
            <template #label>
              <span class="label-with-help">
                {{ t('apiKeys.ipRestrictionLabel') }}
                <el-tooltip :content="t('apiKeys.ipHelp')" placement="top">
                  <el-icon class="label-help-icon"><InfoFilled /></el-icon>
                </el-tooltip>
              </span>
            </template>
            <div class="ip-editor">
              <el-select
                v-model="form.allowedIps"
                multiple
                filterable
                allow-create
                default-first-option
                :reserve-keyword="false"
                collapse-tags
                collapse-tags-tooltip
                :max-collapse-tags="2"
                :placeholder="t('apiKeys.ipPlaceholder')"
                style="width: 100%"
              >
                <el-option v-for="ip in form.allowedIps" :key="ip" :label="ip" :value="ip" />
              </el-select>
              <div class="ip-editor-actions">
                <el-button size="small" text type="primary" :disabled="!clientIp" @click="addCurrentIP">
                  {{ t('apiKeys.ipAddCurrent') }}
                </el-button>
                <el-button size="small" text @click="showDiagnostics = !showDiagnostics">
                  {{ showDiagnostics ? t('apiKeys.ipDiagnosticsHide') : t('apiKeys.ipDiagnosticsShow') }}
                  <el-icon class="ip-diagnostics-caret" :class="{ open: showDiagnostics }"><ArrowDown /></el-icon>
                </el-button>
                <span v-if="clientIp" class="ip-current mono" :title="t('apiKeys.ipCurrent', { ip: clientIp })">
                  {{ t('apiKeys.ipCurrent', { ip: clientIp }) }}
                </span>
                <span v-else class="ip-current is-muted">{{ t('apiKeys.ipUnknown') }}</span>
              </div>
              <div class="ip-status" :class="`is-${proxyDiagnostic.type}`">
                <span class="ip-status-dot" />
                <span class="ip-status-text" :title="proxyDiagnostic.text">{{ proxyDiagnostic.text }}</span>
              </div>
              <el-collapse-transition>
                <div v-show="showDiagnostics" class="ip-diagnostics">
                  <el-alert
                    :type="proxyDiagnostic.type"
                    :closable="false"
                    show-icon
                    class="ip-diagnostic"
                  >
                    <template #title>{{ proxyDiagnostic.text }}</template>
                    <div class="ip-diagnostic-detail">
                      <span>{{ t('apiKeys.ipPeer', { ip: ipInfo.peerIp || '-' }) }}</span>
                      <span v-if="ipInfo.forwardedFor.length">
                        {{ t('apiKeys.ipForwardedFor', { value: ipInfo.forwardedFor.join(', ') }) }}
                      </span>
                      <span v-if="ipInfo.xRealIp">
                        {{ t('apiKeys.ipRealIp', { value: ipInfo.xRealIp }) }}
                      </span>
                      <span v-if="ipInfo.forwardedProto">
                        {{ t('apiKeys.ipForwardedProto', { value: ipInfo.forwardedProto }) }}
                      </span>
                      <span v-if="ipInfo.warnings.length">
                        {{ t('apiKeys.ipWarnings', { value: ipInfo.warnings.join(', ') }) }}
                      </span>
                    </div>
                  </el-alert>
                  <div class="ip-editor-actions">
                    <el-button size="small" text :icon="Refresh" :loading="ipRefreshing" @click="refreshDiagnostics(true)">
                      {{ t('apiKeys.ipRefresh') }}
                    </el-button>
                    <el-button
                      v-if="webrtcSupported"
                      size="small"
                      text
                      type="primary"
                      :loading="webrtcLoading"
                      :title="t('apiKeys.ipWebrtcPrivacy')"
                      @click="detectClientIPs"
                    >
                      {{ t('apiKeys.ipWebrtcDetect') }}
                    </el-button>
                  </div>
                  <div v-if="webrtcResult.length" class="webrtc-result">
                    <div class="form-help">{{ t('apiKeys.ipWebrtcHint') }}</div>
                    <div v-for="item in webrtcResult" :key="item.ip + ':' + item.type" class="webrtc-item">
                      <span class="mono">{{ item.ip }}</span>
                      <el-tag size="small" :type="item.scope === 'public' ? 'success' : 'info'" effect="plain">
                        {{ item.scope === 'public' ? t('apiKeys.ipWebrtcPublic') : t('apiKeys.ipWebrtcLocal') }}
                      </el-tag>
                      <span class="webrtc-type">{{ item.type }}</span>
                      <el-button
                        size="small"
                        text
                        type="primary"
                        :disabled="form.allowedIps.includes(item.ip)"
                        @click="addDetectedIP(item.ip)"
                      >
                        {{ form.allowedIps.includes(item.ip) ? t('apiKeys.ipWebrtcAdded') : t('apiKeys.ipWebrtcAdd') }}
                      </el-button>
                    </div>
                  </div>
                  <div class="form-help">{{ t('apiKeys.ipHelp') }}</div>
                </div>
              </el-collapse-transition>
            </div>
          </el-form-item>
          <el-form-item class="access-item">
            <template #label>
              <span class="label-with-help">
                {{ t('apiKeys.dnsRestrictionLabel') }}
                <el-tooltip :content="t('apiKeys.dnsHelp')" placement="top">
                  <el-icon class="label-help-icon"><InfoFilled /></el-icon>
                </el-tooltip>
              </span>
            </template>
            <el-select
              v-model="form.allowedDnsAccountIds"
              multiple
              filterable
              clearable
              collapse-tags
              collapse-tags-tooltip
              :max-collapse-tags="2"
              :placeholder="t('apiKeys.dnsPlaceholder')"
              style="width: 100%"
            >
              <el-option
                v-for="account in dnsAccounts"
                :key="account.id"
                :label="`${account.name} #${account.id}`"
                :value="account.id"
              />
            </el-select>
          </el-form-item>
        </div>
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
            <div class="scope-filters">
              <el-checkbox v-model="showSelectedOnly" size="small">{{ t('apiKeys.scopeFilterSelected') }}</el-checkbox>
              <el-checkbox v-model="showSensitiveOnly" size="small">{{ t('apiKeys.scopeFilterSensitive') }}</el-checkbox>
              <el-checkbox v-model="showDesc" size="small">{{ t('apiKeys.scopeShowDesc') }}</el-checkbox>
              <span class="scope-sensitive-legend">
                <span class="scope-risk-dot" />
                {{ t('apiKeys.sensitive') }}
              </span>
            </div>
          </div>

          <div class="scope-tabs-row">
            <div v-if="!searchMode" class="scope-tabs" role="tablist" :aria-label="t('apiKeys.permissions')">
              <button
                v-for="module in visibleModules"
                :key="module.value"
                type="button"
                role="tab"
                class="scope-tab"
                :class="{ active: activeModuleView?.value === module.value }"
                :aria-selected="activeModuleView?.value === module.value"
                @click="activeModule = module.value"
              >
                <span>{{ module.label }}</span>
                <span class="scope-tab-count">{{ module.selectedCount }}/{{ module.total }}</span>
              </button>
            </div>
            <span v-else class="scope-results-title">
              {{ t('apiKeys.scopeSearchResults', { count: matchedScopeCount }) }}
            </span>
            <el-tooltip :content="allScopeDesc" placement="top">
              <el-checkbox class="scope-all-toggle" :model-value="isAll" @change="toggleAll">
                <span class="scope-all-label">
                  <el-icon><WarningFilled /></el-icon>
                  {{ allScopeLabel }}
                </span>
              </el-checkbox>
            </el-tooltip>
          </div>

          <div class="scope-panel" :class="{ 'has-desc': showDesc, 'is-locked': isAll }">
            <template v-if="searchMode">
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
                  <span class="scope-group-count">{{ t('apiKeys.scopeMatches', { count: module.scopes.length }) }}</span>
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
              <p v-if="!visibleModules.length" class="scope-empty">{{ t('apiKeys.scopeEmpty') }}</p>
            </template>

            <template v-else-if="activeModuleView">
              <div class="scope-group scope-group-active">
                <div class="scope-group-head">
                  <el-checkbox
                    :model-value="activeModuleView.allSelected"
                    :indeterminate="activeModuleView.someSelected"
                    :disabled="isAll"
                    @change="toggleModule(activeModuleView)"
                  >
                    {{ activeModuleView.label }}
                  </el-checkbox>
                  <span class="scope-group-count">{{ activeModuleView.selectedCount }}/{{ activeModuleView.total }}</span>
                </div>
                <el-checkbox-group v-model="form.scopes" class="scope-grid" @change="handleScopeChange">
                  <el-checkbox
                    v-for="scope in activeModuleView.scopes"
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
                <p v-if="isAll" class="scope-lock-hint">{{ t('apiKeys.scopeLockedHint') }}</p>
              </div>
            </template>

            <p v-else class="scope-empty">
              {{ isAll ? t('apiKeys.scopeLockedHint') : t('apiKeys.scopeEmpty') }}
            </p>
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

    <!-- 密钥日志 / IP 拒绝记录 -->
    <el-dialog
      v-model="logVisible"
      :title="t('apiKeys.logsTitle', { name: logKey?.name || '' })"
      width="min(780px, 94vw)"
    >
      <div class="log-toolbar">
        <el-checkbox v-model="logDeniedOnly" @change="loadKeyLogs">
          {{ t('apiKeys.logsDeniedOnly') }}
        </el-checkbox>
        <el-button size="small" :icon="Refresh" :loading="logLoading" @click="loadKeyLogs">
          {{ t('common.refresh') }}
        </el-button>
      </div>
      <el-table :data="logItems" v-loading="logLoading" row-key="id" max-height="420" style="width: 100%">
        <el-table-column width="90" :label="t('logs.level')">
          <template #default="{ row }">
            <span class="tag-soft" :class="logLevelClass(row.level)">{{ t(logLevelKey(row.level)) }}</span>
          </template>
        </el-table-column>
        <el-table-column min-width="420" :label="t('logs.content')">
          <template #default="{ row }">
            <span class="log-message">{{ row.message }}</span>
          </template>
        </el-table-column>
        <el-table-column width="160" align="right" :label="t('logs.time')">
          <template #default="{ row }">
            <span class="muted">{{ formatDate(row.created_at) }}</span>
          </template>
        </el-table-column>
        <template #empty>
          <div class="log-empty">
            {{ logDeniedOnly ? t('apiKeys.logsDeniedEmpty') : t('apiKeys.logsEmpty') }}
          </div>
        </template>
      </el-table>
      <div class="log-foot muted">{{ t('apiKeys.logsTotal', { count: logTotal }) }}</div>
      <template #footer>
        <el-button @click="logVisible = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>

    <!-- 仅显示一次的令牌 -->
    <el-dialog v-model="tokenVisible" :title="t('apiKeys.tokenTitle')" width="min(860px, 94vw)" :close-on-click-modal="false" @closed="issuedToken = ''">
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
import { ArrowDown, Close, CopyDocument, Delete, InfoFilled, Plus, Refresh, Search, WarningFilled } from '@element-plus/icons-vue'
import { api, copyText } from '../api'
import { useDelayedBatchBar } from '../batchBar'
import { SCOPE_ALL_GROUP, SCOPE_MODULES, moduleOfGroup } from '../scopeCatalog'
import { formatDate, fromNow } from '../utils'
import { detectWebRTCIPs, isWebRTCSupported, type WebRTCIPAddress } from '../webrtcIp'
import type { APIKey, APIKeyCreateResult, APIKeyScope, APIKeyScopeResponse, DNSAccount, LogEntry, LogPage } from '../types'

interface ScopeModuleView {
  value: string
  label: string
  // 当前过滤条件下可见的权限。
  scopes: APIKeyScope[]
  // 模块完整权限数，用于页签计数，避免过滤时把 n/9 显示成 n/n。
  total: number
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
const dnsAccounts = ref<DNSAccount[]>([])
const tableRef = ref<{ clearSelection: () => void } | null>(null)
const selectedIds = ref<number[]>([])
const batchDeleting = ref(false)
const showBatchBar = useDelayedBatchBar(() => selectedIds.value.length > 0)

const formVisible = ref(false)
const editing = ref<APIKey | null>(null)
const form = reactive({
  name: '',
  scopes: [] as string[],
  allowedIps: [] as string[],
  allowedDnsAccountIds: [] as number[],
  expiresAt: null as Date | null,
  enabled: true
})
const clientIp = ref('')
const ipRefreshing = ref(false)
const ipInfo = reactive({
  source: '',
  peerIp: '',
  peerTrusted: false,
  forwardedFor: [] as string[],
  xRealIp: '',
  forwardedProto: '',
  hasForwarded: false,
  warnings: [] as string[]
})
const webrtcSupported = isWebRTCSupported()
const webrtcLoading = ref(false)
const webrtcResult = ref<WebRTCIPAddress[]>([])
// 对话框关闭或重新打开后丢弃迟到的探测结果。
let webrtcRun = 0

const tokenVisible = ref(false)
const issuedToken = ref('')

const logVisible = ref(false)
const logLoading = ref(false)
const logKey = ref<APIKey | null>(null)
const logItems = ref<LogEntry[]>([])
const logTotal = ref(0)
const logDeniedOnly = ref(true)
// 连续切换密钥或筛选时，只保留最后一次请求的结果。
const logRun = { value: 0 }

const scopeMap = computed(() => {
  const map = new Map<string, APIKeyScope>()
  for (const scope of scopes.value) map.set(scope.value, scope)
  return map
})

const scopeQuery = ref('')
const showSelectedOnly = ref(false)
const showSensitiveOnly = ref(false)
const showDesc = ref(readDescPreference())
const activeModule = ref('')
const showDiagnostics = ref(false)

watch(showDesc, (value) => {
  try {
    localStorage.setItem(DESC_PREF_KEY, value ? '1' : '0')
  } catch {
    // 隐私模式下 localStorage 不可用时忽略即可。
  }
})

const allScope = computed(() => scopes.value.find((scope) => scope.group === SCOPE_ALL_GROUP) || null)
const isAll = computed(() => form.scopes.includes('*'))
const allScopeLabel = computed(() => allScope.value?.label || t('apiKeys.scopeAll'))
const allScopeDesc = computed(() => allScope.value?.description || t('apiKeys.scopeAllHelp'))
const searchMode = computed(() => scopeQuery.value.trim().length > 0)

function matchesFilter(scope: APIKeyScope): boolean {
  if (showSelectedOnly.value && !form.scopes.includes(scope.value)) return false
  if (showSensitiveOnly.value && !scope.sensitive) return false
  const query = scopeQuery.value.trim().toLowerCase()
  if (!query) return true
  return [scope.label, scope.description, scope.value].some((text) => text.toLowerCase().includes(query))
}

// 后端用 17 个分组描述权限，这里按资源域收敛成 6 个模块，避免大量只有 1-2 项的分组头。
// 过滤只影响可见列表，页签计数始终基于模块全量，避免“只看已选”时把 3/9 显示成 3/3。
const visibleModules = computed<ScopeModuleView[]>(() => {
  const buckets = new Map<string, APIKeyScope[]>()
  for (const scope of scopes.value) {
    if (scope.group === SCOPE_ALL_GROUP) continue
    const key = moduleOfGroup(scope.group)
    const list = buckets.get(key)
    if (list) list.push(scope)
    else buckets.set(key, [scope])
  }
  const defs = [...SCOPE_MODULES, { value: 'other', labelKey: 'apiKeys.moduleOther', groups: [] }]
  return defs
    .map((def) => {
      const allScopes = buckets.get(def.value) || []
      const list = allScopes.filter(matchesFilter)
      const selectedVisible = list.filter((scope) => form.scopes.includes(scope.value)).length
      const selectedCount = isAll.value
        ? allScopes.length
        : allScopes.filter((scope) => form.scopes.includes(scope.value)).length
      return {
        value: def.value,
        label: t(def.labelKey),
        scopes: list,
        total: allScopes.length,
        selectedCount,
        allSelected: list.length > 0 && (isAll.value || selectedVisible === list.length),
        someSelected: !isAll.value && selectedVisible > 0 && selectedVisible < list.length
      }
    })
    .filter((module) => module.scopes.length > 0)
})

const activeModuleView = computed(() => {
  const list = visibleModules.value
  return list.find((module) => module.value === activeModule.value) || list[0] || null
})

const matchedScopeCount = computed(() => visibleModules.value.reduce((count, module) => count + module.scopes.length, 0))

watch(
  () => visibleModules.value.map((module) => module.value).join('|'),
  () => {
    if (!visibleModules.value.some((module) => module.value === activeModule.value)) {
      activeModule.value = visibleModules.value[0]?.value || ''
    }
  }
)

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

function ipSourceLabel(source: string): string {
  if (source === 'x-forwarded-for') return 'X-Forwarded-For'
  if (source === 'x-real-ip') return 'X-Real-IP'
  return source || '-'
}

// 代理诊断只做展示，真正的 IP 判定仍由后端 netutil.Resolve 完成。
const proxyDiagnostic = computed<{ type: 'info' | 'success' | 'warning'; text: string }>(() => {
  if (ipInfo.hasForwarded && !ipInfo.peerTrusted) {
    return { type: 'warning', text: t('apiKeys.ipProxyIgnored') }
  }
  if (ipInfo.peerTrusted && (ipInfo.source === 'x-forwarded-for' || ipInfo.source === 'x-real-ip')) {
    return { type: 'success', text: t('apiKeys.ipProxyTrusted', { source: ipSourceLabel(ipInfo.source) }) }
  }
  if (ipInfo.peerTrusted) {
    return {
      type: ipInfo.hasForwarded ? 'warning' : 'info',
      text: t('apiKeys.ipProxyTrustedNoHeader')
    }
  }
  return { type: 'info', text: t('apiKeys.ipProxyDirect') }
})

function applyScopeData(data: APIKeyScopeResponse): void {
  scopes.value = data.scopes
  clientIp.value = data.client_ip || ''
  ipInfo.source = data.client_ip_source || ''
  ipInfo.peerIp = data.peer_ip || ''
  ipInfo.peerTrusted = !!data.peer_trusted
  ipInfo.forwardedFor = data.forwarded_for || []
  ipInfo.xRealIp = data.x_real_ip || ''
  ipInfo.forwardedProto = data.forwarded_proto || ''
  ipInfo.hasForwarded = !!data.has_forwarded
  ipInfo.warnings = data.client_ip_warnings || []
}

async function refreshDiagnostics(notify = false): Promise<void> {
  if (ipRefreshing.value) return
  ipRefreshing.value = true
  try {
    const data = await api.get<APIKeyScopeResponse>('/api-keys/scopes')
    applyScopeData(data)
  } catch (error: any) {
    // 自动刷新失败时保留上一次结果；手动刷新时给出提示。
    if (notify) ElMessage.error(error.message)
  } finally {
    ipRefreshing.value = false
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [list, scopeData, accounts] = await Promise.all([
      api.get<APIKey[]>('/api-keys'),
      api.get<APIKeyScopeResponse>('/api-keys/scopes'),
      api.get<DNSAccount[]>('/dns-accounts')
    ])
    items.value = list
    dnsAccounts.value = accounts || []
    applyScopeData(scopeData)
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
  form.allowedIps = []
  form.allowedDnsAccountIds = []
  form.expiresAt = null
  form.enabled = true
  scopeQuery.value = ''
  showSelectedOnly.value = false
  showSensitiveOnly.value = false
  showDiagnostics.value = false
  activeModule.value = ''
  resetWebrtc()
  focusFirstSelectedModule()
  formVisible.value = true
  void refreshDiagnostics()
}

function openEdit(key: APIKey): void {
  editing.value = key
  form.name = key.name
  form.scopes = [...key.scopes]
  form.allowedIps = [...(key.allowed_ips || [])]
  form.allowedDnsAccountIds = [...(key.allowed_dns_account_ids || [])]
  form.expiresAt = key.expires_at ? new Date(key.expires_at) : null
  form.enabled = key.enabled
  scopeQuery.value = ''
  showSelectedOnly.value = false
  showSensitiveOnly.value = false
  showDiagnostics.value = false
  activeModule.value = ''
  resetWebrtc()
  focusFirstSelectedModule()
  formVisible.value = true
  void refreshDiagnostics()
}

// 编辑已有密钥时优先定位到第一个有勾选的模块，避免打开后停在空白页签。
function focusFirstSelectedModule(): void {
  const selected = visibleModules.value.find((module) => module.selectedCount > 0)
  activeModule.value = selected?.value || visibleModules.value[0]?.value || ''
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

function addCurrentIP(): void {
  if (!clientIp.value) return
  if (!form.allowedIps.includes(clientIp.value)) {
    form.allowedIps.push(clientIp.value)
  }
}

function addDetectedIP(ip: string): void {
  if (!form.allowedIps.includes(ip)) form.allowedIps.push(ip)
}

function resetWebrtc(): void {
  webrtcRun++
  webrtcLoading.value = false
  webrtcResult.value = []
}

async function detectClientIPs(): Promise<void> {
  if (webrtcLoading.value) return
  const run = ++webrtcRun
  webrtcLoading.value = true
  try {
    const list = await detectWebRTCIPs()
    // 探测期间对话框已关闭或重新打开时，忽略过期结果。
    if (run !== webrtcRun) return
    webrtcResult.value = list
    if (!list.length) ElMessage.warning(t('apiKeys.ipWebrtcEmpty'))
  } catch (error: any) {
    if (run !== webrtcRun) return
    webrtcResult.value = []
    ElMessage.error(
      error?.message === 'webrtc-unsupported'
        ? t('apiKeys.ipWebrtcUnsupported')
        : t('apiKeys.ipWebrtcFailed')
    )
  } finally {
    if (run === webrtcRun) webrtcLoading.value = false
  }
}

function payload() {
  return {
    name: form.name.trim(),
    scopes: form.scopes,
    allowed_ips: form.allowedIps.map((ip) => ip.trim()).filter(Boolean),
    allowed_dns_account_ids: form.allowedDnsAccountIds,
    enabled: form.enabled,
    expires_at: form.expiresAt ? form.expiresAt.toISOString() : ''
  }
}

function dnsAccountLabel(id: number): string {
  const account = dnsAccounts.value.find((item) => item.id === id)
  return account ? `${account.name} #${account.id}` : `#${id}`
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
  if (editing.value?.allowed_ips?.length && !form.allowedIps.length) {
    try {
      await ElMessageBox.confirm(t('apiKeys.ipClearConfirm'), t('apiKeys.ipRestriction'), {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      })
    } catch (_) {
      return
    }
  }
  if (editing.value?.allowed_dns_account_ids?.length && !form.allowedDnsAccountIds.length) {
    try {
      await ElMessageBox.confirm(t('apiKeys.dnsClearConfirm'), t('apiKeys.dnsRestriction'), {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      })
    } catch (_) {
      return
    }
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

async function openLogs(key: APIKey): Promise<void> {
  logKey.value = key
  logDeniedOnly.value = true
  logVisible.value = true
  await loadKeyLogs()
}

async function loadKeyLogs(): Promise<void> {
  if (!logKey.value) return
  const run = ++logRun.value
  logLoading.value = true
  try {
    const params: Record<string, any> = {
      page: 1,
      size: 50,
      source: 'apikey',
      ref_type: 'apikey',
      ref_id: String(logKey.value.id)
    }
    if (logDeniedOnly.value) params.q = 'ip_denied'
    const data = await api.get<LogPage>('/logs', params)
    if (run !== logRun.value) return
    logItems.value = data.items || []
    logTotal.value = data.total || 0
  } catch (error: any) {
    if (run === logRun.value) ElMessage.error(error.message)
  } finally {
    if (run === logRun.value) logLoading.value = false
  }
}

function logLevelKey(value: string): string {
  const map: Record<string, string> = {
    info: 'logs.info',
    success: 'logs.success',
    warning: 'logs.warning',
    warn: 'logs.warning',
    error: 'logs.error'
  }
  return map[value] || 'logs.info'
}

function logLevelClass(value: string): string {
  const map: Record<string, string> = {
    info: 'tag-blue',
    success: 'tag-green',
    warning: 'tag-amber',
    warn: 'tag-amber',
    error: 'tag-red'
  }
  return map[value] || 'tag-gray'
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
  overflow: hidden;
  font-size: 12px;
  color: #94a3b8;
  text-overflow: ellipsis;
  white-space: nowrap;
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

/*
 * 对话框限高，只让内容区滚动：标题、权限统计和保存按钮始终可见。
 * 4vh + 90vh + 底部 margin 需要控制在 100vh 内。
 */
:global(.api-key-dialog) {
  display: flex;
  flex-direction: column;
  max-height: 90vh;
  margin-bottom: 0;
}

:global(.api-key-dialog .el-dialog__header) {
  flex: none;
}

:global(.api-key-dialog .el-dialog__body) {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding-right: 4px;
  scrollbar-gutter: stable;
}

:global(.api-key-dialog .el-dialog__footer) {
  flex: none;
  margin-top: 8px;
  padding-top: 12px;
  border-top: 1px solid #e2e8f0;
}

/* 基本信息三栏并排，访问限制两栏并排，压缩首屏高度。 */
.form-row {
  display: flex;
  gap: 16px;
}

.form-row :deep(.el-form-item) {
  flex: 1;
  min-width: 0;
}

.form-row :deep(.el-form-item.form-row-name) {
  flex: 1.5;
}

.form-row :deep(.el-form-item.form-row-enabled) {
  flex: 0 0 120px;
}

.form-row-access {
  margin-top: 2px;
}

.label-with-help {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.label-help-icon {
  font-size: 13px;
  color: #94a3b8;
  cursor: help;
}

.label-help-icon:hover {
  color: #1d4ed8;
}

.ip-editor {
  width: 100%;
}

.ip-editor-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 10px;
  margin: 2px 0 0;
}

.ip-editor-actions :deep(.el-button) {
  margin-left: 0;
}

.ip-current {
  max-width: 60%;
  margin-left: auto;
  overflow: hidden;
  font-size: 12px;
  color: #64748b;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ip-current.is-muted {
  color: #b45309;
}

.ip-status {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.5;
  color: #64748b;
}

.ip-status-dot {
  flex: none;
  width: 6px;
  height: 6px;
  margin-top: 6px;
  background: #94a3b8;
}

.ip-status.is-success .ip-status-dot {
  background: #10b981;
}

.ip-status.is-warning {
  color: #b45309;
}

.ip-status.is-warning .ip-status-dot {
  background: #f59e0b;
}

.ip-status-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ip-diagnostics {
  margin-top: 8px;
}

.ip-diagnostics-caret {
  margin-left: 4px;
  transition: transform 0.2s ease;
}

.ip-diagnostics-caret.open {
  transform: rotate(180deg);
}

.ip-diagnostic {
  margin: 0 0 6px;
}

.ip-diagnostics .form-help {
  margin-top: 6px;
}

.ip-diagnostic-detail {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 14px;
  font-size: 12px;
  line-height: 1.6;
}

.log-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  margin-bottom: 10px;
}

.log-foot {
  margin-top: 10px;
  font-size: 12px;
  text-align: right;
}

.log-empty {
  padding: 32px 0;
  color: #94a3b8;
  text-align: center;
}

.log-message {
  word-break: break-word;
}

.webrtc-result {
  margin: 6px 0 8px;
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
}

.webrtc-item {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 2px 0;
}

.webrtc-type {
  font-size: 12px;
  color: #94a3b8;
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
  flex: 1 1 220px;
  width: auto;
  max-width: 320px;
}

.scope-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0 12px;
  margin-left: auto;
}

.scope-filters :deep(.el-checkbox) {
  height: 24px;
  margin-right: 0;
}

.scope-sensitive-legend {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: #94a3b8;
}

/* 模块页签：一次只展开一个模块，权限区高度稳定，阅读顺序一致。 */
.scope-tabs-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  border-bottom: 1px solid #e2e8f0;
}

.scope-tabs {
  display: flex;
  flex: 1 1 auto;
  min-width: 0;
  gap: 2px;
  overflow-x: auto;
  scrollbar-width: none;
}

.scope-tabs::-webkit-scrollbar {
  display: none;
}

.scope-tab {
  flex: none;
  padding: 7px 10px;
  border: 0;
  background: transparent;
  color: #475569;
  font-size: 13px;
  white-space: nowrap;
  cursor: pointer;
}

.scope-tab:hover {
  color: #1d4ed8;
  background: #f8fafc;
}

.scope-tab.active {
  color: #1d4ed8;
  font-weight: 600;
  box-shadow: inset 0 -2px 0 #1d4ed8;
}

.scope-tab-count {
  margin-left: 6px;
  font-size: 11px;
  font-weight: 400;
  color: #94a3b8;
  font-variant-numeric: tabular-nums;
}

.scope-results-title {
  flex: 1 1 auto;
  padding: 7px 0;
  font-size: 13px;
  color: #475569;
}

.scope-all-toggle {
  flex: none;
  height: 28px;
  margin-right: 0;
}

.scope-all-toggle :deep(.el-checkbox__label) {
  padding-left: 6px;
}

.scope-all-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  font-weight: 600;
  color: #b45309;
}

.scope-all-label .el-icon {
  color: #d97706;
}

.scope-panel {
  width: 100%;
  height: clamp(180px, 26vh, 260px);
  overflow-y: auto;
  padding: 4px 6px 4px 2px;
  line-height: 1.5;
  border: 1px solid #eef2f7;
  background: #fcfdff;
}

.scope-panel.is-locked .scope-group,
.scope-panel.is-locked .scope-lock-hint {
  opacity: 0.6;
}

.scope-group {
  margin-bottom: 10px;
  padding-bottom: 6px;
  border-bottom: 1px solid #f1f5f9;
}

.scope-group:last-child {
  margin-bottom: 0;
  padding-bottom: 0;
  border-bottom: 0;
}

.scope-group-active {
  margin-bottom: 0;
  padding-bottom: 0;
  border-bottom: 0;
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
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
  gap: 2px 16px;
  align-content: start;
}

.scope-panel.has-desc .scope-grid {
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
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

@media (max-width: 900px) {
  .form-row {
    flex-wrap: wrap;
  }

  .form-row :deep(.el-form-item.form-row-name),
  .form-row :deep(.el-form-item.form-row-expires) {
    flex: 1 1 260px;
  }

  .scope-search {
    max-width: none;
  }

  .scope-grid,
  .scope-panel.has-desc .scope-grid {
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  }
}

@media (max-width: 720px) {
  .form-row {
    display: block;
  }

  .scope-toolbar {
    display: block;
  }

  .scope-filters {
    margin-top: 6px;
    margin-left: 0;
  }

  .scope-tabs-row {
    flex-wrap: wrap;
  }

  .scope-panel {
    height: auto;
    max-height: 40vh;
  }

  .ip-current {
    max-width: none;
    margin-left: 0;
  }
}

@media (max-width: 600px) {
  .scope-grid,
  .scope-panel.has-desc .scope-grid {
    grid-template-columns: 1fr;
  }

  .scope-footer {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .scope-footer-actions {
    margin-left: 0;
  }

  .scope-footer-actions :deep(.el-button) {
    flex: 1;
    margin-left: 0;
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
