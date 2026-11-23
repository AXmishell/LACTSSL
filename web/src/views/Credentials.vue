<template>
  <div class="page table-page cred-page">
    <div class="page-head">
      <div>
        <h1>{{ isDNS ? t('credentials.dnsTitle') : t('credentials.deployTitle') }}</h1>
        <p>{{ isDNS ? t('credentials.dnsSubtitle') : t('credentials.deploySubtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle @click="load" />
        <el-button v-if="!isDNS" :icon="Document" @click="openRecords">{{ t('credentials.recentDeployments') }}</el-button>
        <el-button type="primary" :icon="Plus" @click="openCreate">{{ isDNS ? t('credentials.addDns') : t('credentials.addDeploy') }}</el-button>
      </div>
    </div>

    <div v-if="!items.length && !loading" class="k-card table-fill cred-empty-card">
      <el-icon size="34" color="#94a3b8"><component :is="isDNS ? DnsAccountIcon : UploadFilled" /></el-icon>
      <h3 style="margin: 14px 0 6px">{{ isDNS ? t('credentials.emptyDnsTitle') : t('credentials.emptyDeployTitle') }}</h3>
      <p class="muted" style="margin-top: 0">{{ isDNS ? t('credentials.emptyDnsDesc') : t('credentials.emptyDeployDesc') }}</p>
      <el-button type="primary" style="margin-top: 12px" @click="openCreate">{{ t('credentials.addNow') }}</el-button>
    </div>

    <div v-else class="k-card cred-list table-fill" v-loading="loading">
      <div class="cred-list-head">
        <span class="cred-head-name">
          <el-checkbox
            :model-value="allSelected"
            :indeterminate="someSelected"
            :disabled="!items.length"
            @change="toggleAll"
          />
          <span>{{ t('common.name') }}</span>
        </span>
        <span>{{ t('common.remark') }}</span>
        <span>{{ t('credentials.time') }}</span>
        <span class="cred-head-actions">{{ t('common.actions') }}</span>
      </div>
      <div class="cred-scroll">
      <div v-for="item in items" :key="item.id" class="cred-row" :class="{ 'is-selected': isSelected(item) }">
        <div class="cred-cell cred-name">
          <el-checkbox
            class="cred-check"
            :model-value="isSelected(item)"
            @change="(value: string | number | boolean) => toggleItem(item, Boolean(value))"
          />
          <div class="cred-icon" :class="{ 'has-logo': logoFor(item) }">
            <img v-if="logoFor(item)" :src="logoFor(item)" :alt="typeLabel(item)" loading="lazy" @error="markIconBroken(item)" />
            <template v-else>{{ initials(item.name) }}</template>
          </div>
          <div class="cred-name-text">
            <div class="cred-title">{{ item.name }}</div>
            <div class="cred-meta" style="margin-top: 4px">
              <span class="tag-soft tag-blue">{{ typeLabel(item) }}</span>
              <el-tag
                v-if="item.verify_status"
                :type="verifyTagType(item.verify_status)"
                size="small"
                effect="light"
                round
                style="margin-left: 6px"
                :title="item.verify_message || ''"
              >
                {{ verifyStatusLabel(item.verify_status) }}
              </el-tag>
              <el-tag v-else size="small" effect="plain" round style="margin-left: 6px">
                {{ t('credentials.verifyNever') }}
              </el-tag>
            </div>
          </div>
        </div>
        <div class="cred-cell cred-remark cred-meta">{{ item.remark || t('credentials.noRemark') }}</div>
        <div class="cred-cell cred-time cred-meta">{{ t('credentials.updatedAt', { value: fromNow(item.updated_at) }) }}</div>
        <div class="cred-cell cred-actions">
          <el-button v-if="isDNS" text type="primary" size="small" @click="openZones(item)">
            {{ t('credentials.zones') }}
          </el-button>
          <el-button
            text
            type="primary"
            size="small"
            :loading="verifying && verifyTarget?.id === item.id"
            @click="openVerify(item)"
          >
            {{ t('credentials.verify') }}
          </el-button>
          <el-button text type="primary" size="small" @click="openEdit(item)">{{ t('credentials.configure') }}</el-button>
          <el-dropdown trigger="click" @command="(cmd: string) => handleCommand(cmd, item)">
            <el-button text circle><el-icon><MoreFilled /></el-icon></el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="edit">{{ t('credentials.edit') }}</el-dropdown-item>
                <el-dropdown-item command="delete" divided>{{ t('common.delete') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
      </div>

      <div v-if="showBatchBar && selectedIds.length" class="cred-footer">
        <div class="batch-bar">
          <div class="batch-actions">
            <el-button size="small" type="danger" plain :icon="Delete" :loading="batchDeleting" @click="batchDelete">
              {{ t('credentials.batchDelete') }}
            </el-button>
            <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('credentials.clearSelection') }}</el-button>
          </div>
          <span class="batch-count">{{ t('credentials.selectedCount', { count: selectedIds.length }) }}</span>
        </div>
      </div>
    </div>

    <el-dialog v-model="dialogVisible" :title="`${editingId ? t('credentials.edit') : t('credentials.add')} · ${isDNS ? t('credentials.dnsTitle') : t('credentials.deployTitle')}`" width="600px" destroy-on-close>
      <el-form :model="form" label-position="top">
        <el-form-item :label="isDNS ? t('credentials.accountName') : t('credentials.deployName')" required>
          <el-input v-model="form.name" :placeholder="isDNS ? t('credentials.namePlaceholderDns') : t('credentials.namePlaceholderDeploy')" maxlength="60" />
        </el-form-item>
        <el-form-item :label="isDNS ? t('credentials.dnsProvider') : t('credentials.deployType')" required>
          <el-select
            v-model="typeValue"
            style="width: 100%"
            filterable
            popper-class="provider-select-popper"
            @change="onTypeChange"
          >
            <el-option
              v-for="option in typeOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
              :disabled="option.available === false"
            >
              <div class="provider-option">
                <span class="provider-option-label">{{ option.label }}</span>
                <span class="provider-option-desc">
                  {{
                    option.available === false
                      ? t('credentials.providerUnavailable')
                      : option.desc
                  }}
                </span>
              </div>
            </el-option>
          </el-select>
        </el-form-item>

        <el-alert
          v-if="providerMissing"
          :title="t('credentials.providerMissingHint')"
          type="warning"
          :closable="false"
          style="margin-bottom: 18px"
        />
        <el-alert
          v-else-if="providerUnavailable"
          :title="t('credentials.providerUnavailableHint')"
          type="warning"
          :closable="false"
          style="margin-bottom: 18px"
        />
        <el-alert v-if="activeDef?.desc" :title="activeDef.desc" type="info" :closable="false" style="margin-bottom: 18px" />
        <div v-if="!isDNS" class="config-tip" style="margin-bottom: 18px">{{ t('credentials.deployAuthOnly') }}</div>

        <template v-for="field in visibleFields" :key="field.key">
          <el-form-item :label="field.label" :required="field.required">
            <el-select v-if="field.type === 'select'" v-model="form.config[field.key]" style="width: 100%" filterable>
              <el-option v-for="option in field.options || []" :key="option.value" :label="option.label" :value="option.value" />
            </el-select>
            <el-input
              v-else-if="field.type === 'textarea'"
              v-model="form.config[field.key]"
              type="textarea"
              :rows="5"
              :placeholder="field.placeholder"
              class="mono"
            />
            <el-input
              v-else-if="field.type === 'password'"
              v-model="form.config[field.key]"
              type="password"
              show-password
              :placeholder="field.placeholder"
            />
            <el-input
              v-else-if="field.type === 'number'"
              v-model="form.config[field.key]"
              :placeholder="field.placeholder"
            />
            <el-input v-else v-model="form.config[field.key]" :placeholder="field.placeholder" />
            <div v-if="field.secret && editingId" class="form-help">{{ t('credentials.secretKeepHint') }}</div>
            <div v-if="field.help" class="form-help">{{ field.help }}</div>
          </el-form-item>
        </template>

        <el-form-item :label="t('credentials.remark')">
          <el-input v-model="form.remark" :placeholder="t('credentials.remarkPlaceholder')" maxlength="100" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('credentials.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ t('credentials.save') }}</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="verifyVisible"
      :title="`${t('credentials.verify')} · ${verifyTarget?.name || ''}`"
      width="520px"
      destroy-on-close
      @closed="resetVerify"
    >
      <el-form label-position="top">
        <div class="config-tip" style="margin-bottom: 18px">{{ t('credentials.verifySavedHint') }}</div>
        <el-form-item v-if="isDNS" :label="t('credentials.verifyDomain')">
          <el-input
            v-model="verifyDomain"
            :placeholder="t('credentials.verifyDomainPlaceholder')"
            :disabled="verifying"
            @keyup.enter="verifyCurrent"
          />
          <div class="form-help">{{ t('credentials.verifyDomainHelp') }}</div>
        </el-form-item>

        <el-alert
          v-if="verifyResult"
          :type="verifyResult.ok ? (verifyResult.mode === 'scope' ? 'info' : 'success') : 'warning'"
          :closable="false"
          style="margin-bottom: 12px"
          :title="verifyResultTitle"
        >
          <div v-for="check in visibleVerifyChecks" :key="check.key" class="verify-check">
            <el-tag :type="verifyTagType(check.status === 'ok' ? 'ok' : check.status === 'unsupported' ? 'unsupported' : 'failed')" size="small" effect="light">
              {{ verifyStatusLabel(check.status === 'ok' ? 'ok' : check.status === 'unsupported' ? 'unsupported' : check.status === 'skipped' ? 'skipped' : 'failed') }}
            </el-tag>
            <span class="verify-check-label">{{ verifyCheckLabel(check.key) }}</span>
            <span v-if="check.message" class="muted verify-check-message">{{ check.message }}</span>
          </div>
        </el-alert>
      </el-form>
      <template #footer>
        <el-button :disabled="verifying" @click="verifyVisible = false">{{ t('credentials.cancel') }}</el-button>
        <el-button type="primary" :loading="verifying" @click="verifyCurrent">{{ t('credentials.verify') }}</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="zonesVisible"
      :title="`${t('credentials.zones')} · ${zonesTarget?.name || ''}`"
      width="680px"
      class="zones-dialog"
      destroy-on-close
      :before-close="beforeZonesClose"
      @closed="resetZones"
    >
      <div class="zone-hint">
        <span>{{ t('credentials.zonesHint') }}</span>
        <el-tooltip :content="t('credentials.zonesHintDetail')" placement="top" :show-after="200">
          <el-icon class="zone-hint-icon"><InfoFilled /></el-icon>
        </el-tooltip>
      </div>

      <div v-loading="zonesLoading" class="zone-list">
        <div class="zone-head">
          <span>{{ t('credentials.colZone') }}</span>
          <span>{{ t('credentials.colMatch') }}</span>
          <span>{{ t('credentials.colPriority') }}</span>
          <span>{{ t('credentials.colEnabled') }}</span>
          <span />
        </div>
        <div v-if="!zoneRows.length" class="zone-empty">{{ t('credentials.zonesEmpty') }}</div>
        <div
          v-for="(row, index) in zoneRows"
          :key="row.uid"
          class="zone-row"
          :class="{ 'is-off': !row.enabled }"
        >
          <div class="zone-cell">
            <el-input
              v-model="row.zone"
              :placeholder="t('credentials.zonePlaceholder')"
              :aria-label="t('credentials.colZone')"
              @input="clearZoneError(row)"
            />
            <div v-if="zoneErrors[row.uid]" class="zone-error">{{ zoneErrors[row.uid] }}</div>
          </div>
          <el-select v-model="row.match_mode" :aria-label="t('credentials.colMatch')">
            <el-option :label="t('credentials.zoneSuffix')" value="suffix" />
            <el-option :label="t('credentials.zoneExact')" value="exact" />
          </el-select>
          <el-input-number
            v-model="row.priority"
            :min="-1000"
            :max="1000"
            controls-position="right"
            :aria-label="t('credentials.colPriority')"
          />
          <div class="zone-row-end">
            <el-switch v-model="row.enabled" :aria-label="t('credentials.colEnabled')" />
            <el-tooltip :content="t('credentials.zoneRemove')" placement="top">
              <el-button
                text
                type="danger"
                :icon="Delete"
                :aria-label="t('credentials.zoneRemove')"
                @click="removeZoneRow(index)"
              />
            </el-tooltip>
          </div>
        </div>
      </div>

      <div class="zone-actions">
        <el-button :icon="Plus" @click="addZoneRow">{{ t('credentials.zoneAdd') }}</el-button>
        <span class="zone-actions-spacer" />
        <el-input
          v-model="probeDomain"
          class="zone-probe-input"
          :placeholder="t('credentials.zoneProbePlaceholder')"
          :aria-label="t('credentials.zoneProbePlaceholder')"
          @keyup.enter="probeZone"
        />
        <el-button type="primary" plain :icon="Search" :loading="probing" @click="probeZone">
          {{ t('credentials.zoneProbeAction') }}
        </el-button>
        <el-tooltip :content="t('credentials.zoneProbeHint')" placement="top" :show-after="200">
          <el-icon class="zone-hint-icon"><InfoFilled /></el-icon>
        </el-tooltip>
      </div>
      <template #footer>
        <el-button @click="cancelZones">{{ t('credentials.cancel') }}</el-button>
        <el-button type="primary" :loading="zonesSaving" @click="saveZones">{{ t('credentials.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 最近部署记录：与任务执行日志一致，使用抽屉展示 -->
    <el-drawer
      v-model="recordsVisible"
      :title="t('credentials.recentDeployments')"
      size="560px"
    >
      <div v-loading="recordsLoading">
        <div class="records-hint muted">{{ t('credentials.recentDeploymentsSub') }}</div>
        <div v-if="!records.length" class="muted records-empty">{{ t('credentials.noRecords') }}</div>
        <div v-for="row in records" :key="row.id" class="log-row">
          <div class="log-dot" :style="{ background: recordColor(row) }"></div>
          <div class="log-body">
            <div class="log-msg">{{ row.message }}</div>
            <div class="log-time">
              {{ formatDate(row.created_at) }} · {{ row.account_name || t('credentials.unknownTarget') }} ·
              {{ certDomain(row.cert_id) }}
            </div>
          </div>
          <el-tag :type="row.status === 'success' ? 'success' : 'danger'" size="small" effect="plain">
            {{ row.status === 'success' ? t('credentials.success') : t('credentials.failed') }}
          </el-tag>
        </div>
      </div>
      <template #footer>
        <div class="records-drawer-footer">
          <el-button @click="loadRecords" :loading="recordsLoading">{{ t('credentials.refresh') }}</el-button>
          <el-button
            type="danger"
            plain
            :icon="Delete"
            :loading="clearingRecords"
            :disabled="!records.length"
            @click="clearRecords"
          >
            {{ t('credentials.clearRecords') }}
          </el-button>
        </div>
      </template>
    </el-drawer>

    <OverflowTooltipLayer />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Close, Delete, Document, InfoFilled, MoreFilled, Plus, Refresh, Search, UploadFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { useDelayedBatchBar } from '../batchBar'
import { loadMeta } from '../store'
import { fieldVisible, formatDate, fromNow } from '../utils'
import { loadPluginIcons, pluginIcon } from '../pluginIcons'
import DnsAccountIcon from '../components/DnsAccountIcon.vue'
import OverflowTooltipLayer from '../components/OverflowTooltipLayer.vue'
import type { Certificate, DeployRecord, DNSZoneBinding, FieldDef, Meta, ProviderDef, VerifyResult } from '../types'

interface CredentialItem {
  id: number
  name: string
  provider?: string
  type?: string
  config: Record<string, string>
  remark: string
  verify_status?: string
  verify_message?: string
  verify_mode?: string
  verified_at?: string | null
  updated_at: string
}

interface CredentialForm {
  name: string
  type: string
  config: Record<string, string>
  remark: string
}

const route = useRoute()
const { t } = useI18n()
const isDNS = computed(() => route.meta.tab !== 'deploy')

const loading = ref(false)
const saving = ref(false)
const items = ref<CredentialItem[]>([])
const meta = ref<Meta | null>(null)
const records = ref<DeployRecord[]>([])
const certificates = ref<Certificate[]>([])
const recordsVisible = ref(false)
const recordsLoading = ref(false)
const clearingRecords = ref(false)
const dialogVisible = ref(false)
const editingId = ref(0)
const verifyVisible = ref(false)
const verifyTarget = ref<CredentialItem | null>(null)
const selectedIds = ref<number[]>([])
const showBatchBar = useDelayedBatchBar(() => selectedIds.value.length > 0)
const batchDeleting = ref(false)
const verifying = ref(false)
const verifyResult = ref<VerifyResult | null>(null)
// 凭据级验证不包含部署目标参数，后端会返回 scope=skipped 说明“未校验目标”。
// 该分项不是失败项，展示出来容易被误读为缺少配置，因此只在凭据验证结果中隐藏。
const visibleVerifyChecks = computed(() =>
  (verifyResult.value?.checks || []).filter((check) => !(check.key === 'scope' && check.status === 'skipped'))
)
const verifyResultTitle = computed(() => {
  const result = verifyResult.value
  if (!result) return ''
  if (!result.ok) return t('credentials.verifyFailed')
  // scope 模式只校验凭据配置与调用权限，没有真的探测服务商，不能展示“凭据可用”。
  if (result.mode === 'scope') return t('credentials.verifyPermissionTitle')
  return t('credentials.verifyPassed')
})
const verifyDomain = ref('')

// 域名绑定：声明式解析 CSR 域名的 DNS 账户归属。
interface ZoneRow {
  uid: string
  zone: string
  match_mode: 'suffix' | 'exact'
  priority: number
  enabled: boolean
}
const zonesVisible = ref(false)
const zonesTarget = ref<CredentialItem | null>(null)
const zonesLoading = ref(false)
const zonesSaving = ref(false)
const zoneRows = ref<ZoneRow[]>([])
const zoneErrors = ref<Record<string, string>>({})
const zonesSnapshot = ref('')
const probeDomain = ref('')
const probing = ref(false)

let zoneUidSeq = 0
function nextZoneUid(): string {
  zoneUidSeq += 1
  return `zone-${zoneUidSeq}`
}

function normalizeZoneKey(zone: string): string {
  // 与服务端 NormalizeDNSZone 保持一致：通配符前缀和尾点都会归一化掉。
  return zone.trim().toLowerCase().replace(/^\*\./, '').replace(/\.+$/, '')
}

// 只比较会影响提交结果的字段，避免输入框临时状态触发“未保存”提示。
function serializeZones(rows: ZoneRow[]): string {
  return JSON.stringify(
    rows.map((row) => ({
      zone: normalizeZoneKey(row.zone),
      match_mode: row.match_mode,
      priority: row.priority || 0,
      enabled: row.enabled
    }))
  )
}

const zonesDirty = computed(() => serializeZones(zoneRows.value) !== zonesSnapshot.value)

const form = reactive<CredentialForm>({ name: '', type: '', config: {}, remark: '' })

const allSelected = computed(() => items.value.length > 0 && selectedIds.value.length === items.value.length)
const someSelected = computed(() => selectedIds.value.length > 0 && !allSelected.value)

const typeOptions = computed<ProviderDef[]>(() => (isDNS.value ? meta.value?.dns_providers : meta.value?.deploy_types) || [])
const activeDef = computed(() => typeOptions.value.find((item) => item.value === form.type))
const providerUnavailable = computed(() => activeDef.value?.available === false)
// 历史账户可能引用当前不可用的服务商或部署目标（未编译、插件未安装或已停用）；
// 这些条目不会出现在列表中，但元数据与账户配置仍保留在服务端。
const providerMissing = computed(() => !!form.type && !activeDef.value)

const visibleFields = computed<FieldDef[]>(() => {
  const fields = activeDef.value?.fields || []
  return fields.filter((field) => fieldVisible(field, fields, form.config))
})

const typeValue = computed({
  get: () => form.type,
  set: (value: string) => {
    form.type = value
  }
})

// 每次打开或关闭验证弹窗后清空上次结果，避免结果与当前凭据错配。
function resetVerify(): void {
  verifyResult.value = null
  verifyDomain.value = ''
  verifyTarget.value = null
}

// 验证只针对已保存的凭据，避免使用表单中尚未保存的配置。
// 打开弹窗后立即执行一次验证，用户无需再点击一次“验证可用性”。
function openVerify(item: CredentialItem): void {
  resetVerify()
  verifyTarget.value = item
  verifyVisible.value = true
  void verifyCurrent()
}

function verifyTagType(status: string): 'success' | 'danger' | 'warning' | 'info' {
  switch (status) {
    case 'ok':
      return 'success'
    case 'failed':
    case 'error':
      return 'danger'
    case 'unsupported':
      return 'warning'
    default:
      return 'info'
  }
}

function verifyStatusLabel(status: string): string {
  switch (status) {
    case 'ok':
      return t('credentials.verifyOk')
    case 'failed':
    case 'error':
      return t('credentials.verifyFail')
    case 'unsupported':
      return t('credentials.verifyUnsupported')
    case 'skipped':
      return t('credentials.verifySkipped')
    default:
      return t('credentials.verifyNever')
  }
}

function verifyCheckLabel(key: string): string {
  const path = `credentials.verifyCheck.${key}`
  const label = t(path)
  return label === path ? key : label
}

async function verifyCurrent(): Promise<void> {
  const target = verifyTarget.value
  if (!target) return
  verifying.value = true
  verifyResult.value = null
  try {
    const payload: Record<string, unknown> = {
      mode: 'auto'
    }
    let path = ''
    if (isDNS.value) {
      payload.domain = verifyDomain.value.trim()
      // 已保存账户只使用数据库中的配置，避免“旧密钥 + 新地址”外带凭据。
      path = `/dns-accounts/${target.id}/verify`
    } else {
      path = `/deploy-accounts/${target.id}/verify`
    }
    const data = await api.post<VerifyResult>(path, payload)
    if (data === null) return
    verifyResult.value = data
    await load()
    if (data.ok && data.mode === 'scope') ElMessage.info(data.message || t('credentials.verifyPermissionOnly'))
    else if (data.ok) ElMessage.success(t('credentials.verifyPassed'))
    else ElMessage.warning(data.message || t('credentials.verifyFailed'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    verifying.value = false
  }
}

function openZones(item: CredentialItem): void {
  resetZones()
  zonesTarget.value = item
  zonesVisible.value = true
  void loadZones(item)
}

function toZoneRows(list: DNSZoneBinding[] | null): ZoneRow[] {
  return (list || []).map((zone) => ({
    uid: nextZoneUid(),
    zone: zone.zone,
    match_mode: zone.match_mode === 'exact' ? 'exact' : 'suffix',
    priority: zone.priority || 0,
    enabled: zone.enabled
  }))
}

async function loadZones(item: CredentialItem): Promise<void> {
  zonesLoading.value = true
  try {
    const list = await api.get<DNSZoneBinding[]>(`/dns-accounts/${item.id}/zones`)
    zoneRows.value = toZoneRows(list)
    zoneErrors.value = {}
    zonesSnapshot.value = serializeZones(zoneRows.value)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    zonesLoading.value = false
  }
}

function resetZones(): void {
  zonesTarget.value = null
  zoneRows.value = []
  zoneErrors.value = {}
  zonesSnapshot.value = serializeZones([])
  probeDomain.value = ''
}

function addZoneRow(): void {
  zoneRows.value.push({ uid: nextZoneUid(), zone: '', match_mode: 'suffix', priority: 0, enabled: true })
}

function removeZoneRow(index: number): void {
  const [removed] = zoneRows.value.splice(index, 1)
  if (!removed) return
  const next = { ...zoneErrors.value }
  delete next[removed.uid]
  zoneErrors.value = next
}

function clearZoneError(row: ZoneRow): void {
  if (!zoneErrors.value[row.uid]) return
  const next = { ...zoneErrors.value }
  delete next[row.uid]
  zoneErrors.value = next
}

// 保存前逐行校验，避免空行被静默丢弃后仍提示成功。
function validateZones(): boolean {
  const errors: Record<string, string> = {}
  const seen = new Map<string, string>()
  for (const row of zoneRows.value) {
    const zone = row.zone.trim()
    if (!zone) {
      errors[row.uid] = t('credentials.zoneRequired')
      continue
    }
    if (/\s/.test(zone) || zone.includes('/')) {
      errors[row.uid] = t('credentials.zoneInvalid')
      continue
    }
    const key = normalizeZoneKey(zone)
    if (!key || key.includes('*')) {
      errors[row.uid] = t('credentials.zoneInvalid')
      continue
    }
    const previous = seen.get(key)
    if (previous) {
      errors[row.uid] = t('credentials.zoneDuplicateRow')
      if (!errors[previous]) errors[previous] = t('credentials.zoneDuplicateRow')
      continue
    }
    seen.set(key, row.uid)
  }
  zoneErrors.value = errors
  return Object.keys(errors).length === 0
}

async function confirmDiscardZones(): Promise<boolean> {
  if (!zonesDirty.value) return true
  try {
    await ElMessageBox.confirm(t('credentials.zonesDiscardConfirm'), t('credentials.zonesUnsaved'), {
      confirmButtonText: t('credentials.zonesDiscard'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
    return true
  } catch (_) {
    return false
  }
}

async function cancelZones(): Promise<void> {
  if (await confirmDiscardZones()) zonesVisible.value = false
}

async function beforeZonesClose(done: () => void): Promise<void> {
  if (zonesSaving.value) return
  if (await confirmDiscardZones()) done()
}

async function saveZones(): Promise<void> {
  const target = zonesTarget.value
  if (!target) return
  if (!validateZones()) {
    ElMessage.warning(t('credentials.zonesFixErrors'))
    return
  }
  const zones = zoneRows.value.map((row) => ({
    zone: row.zone.trim(),
    match_mode: row.match_mode,
    priority: row.priority || 0,
    enabled: row.enabled
  }))
  zonesSaving.value = true
  try {
    const saved = await api.put<DNSZoneBinding[]>(`/dns-accounts/${target.id}/zones`, { zones })
    if (saved === null) return
    zoneRows.value = toZoneRows(saved)
    zoneErrors.value = {}
    zonesSnapshot.value = serializeZones(zoneRows.value)
    ElMessage.success(t('credentials.zonesSaved'))
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    zonesSaving.value = false
  }
}

async function probeZone(): Promise<void> {
  const target = zonesTarget.value
  if (!target) return
  const domain = probeDomain.value.trim()
  if (!domain) {
    ElMessage.warning(t('credentials.zoneProbeRequired'))
    return
  }
  probing.value = true
  try {
    // apply=false：探测只做只读检查，结果写入草稿，由“保存”统一提交。
    const data = await api.post<{ zone: string; applied: boolean }>(`/dns-accounts/${target.id}/zones/probe`, {
      domain,
      mode: 'read',
      apply: false
    })
    if (data?.zone) {
      const zone = data.zone.trim()
      const key = normalizeZoneKey(zone)
      if (zoneRows.value.some((row) => normalizeZoneKey(row.zone) === key)) {
        ElMessage.warning(t('credentials.zoneDuplicateRow'))
      } else {
        zoneRows.value.push({ uid: nextZoneUid(), zone, match_mode: 'suffix', priority: 0, enabled: true })
        ElMessage.success(t('credentials.zoneProbeSuccess', { zone }))
      }
      probeDomain.value = ''
    } else {
      ElMessage.warning(t('credentials.zoneProbeUnsupported'))
    }
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    probing.value = false
  }
}

function initials(name: string): string {
  return (name || '?').slice(0, 1).toUpperCase()
}

function credentialIconKind(): 'dns' | 'deploy' {
  return isDNS.value ? 'dns' : 'deploy'
}

function credentialIconValue(item: CredentialItem): string {
  return (isDNS.value ? item.provider : item.type) || ''
}

// 损坏的 data URI（第三方仓库/旧缓存）直接回退首字母，避免显示破图。
const brokenIcons = reactive(new Set<string>())

function logoFor(item: CredentialItem): string {
  const value = credentialIconValue(item)
  if (!value || brokenIcons.has(`${credentialIconKind()}:${value}`)) return ''
  return pluginIcon(credentialIconKind(), value)
}

function markIconBroken(item: CredentialItem): void {
  const value = credentialIconValue(item)
  if (value) brokenIcons.add(`${credentialIconKind()}:${value}`)
}

function typeLabel(item: CredentialItem): string {
  const value = isDNS.value ? item.provider : item.type
  const list = isDNS.value ? meta.value?.dns_providers : meta.value?.deploy_types
  return (list || []).find((option) => option.value === value)?.label || value || ''
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [metaData, list] = await Promise.all([
      loadMeta(true),
      api.get<CredentialItem[]>(isDNS.value ? '/dns-accounts' : '/deploy-accounts')
    ])
    meta.value = metaData
    items.value = list || []
    selectedIds.value = []
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

function openRecords(): void {
  recordsVisible.value = true
  void loadRecords()
}

async function loadRecords(): Promise<void> {
  recordsLoading.value = true
  try {
    const [deployments, certs] = await Promise.all([
      api.get<DeployRecord[]>('/deployments'),
      api.get<Certificate[]>('/certificates')
    ])
    records.value = deployments || []
    certificates.value = certs || []
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    recordsLoading.value = false
  }
}

function recordColor(row: DeployRecord): string {
  return row.status === 'success' ? '#10b981' : '#ef4444'
}

async function clearRecords(): Promise<void> {
  if (!records.value.length) return
  try {
    await ElMessageBox.confirm(t('credentials.clearRecordsConfirm'), t('credentials.clearRecordsTitle'), {
      confirmButtonText: t('credentials.clearRecords'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  clearingRecords.value = true
  try {
    const data = await api.delete<{ deleted: number }>('/deployments')
    ElMessage.success(t('credentials.recordsCleared', { count: data?.deleted || 0 }))
    records.value = []
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    clearingRecords.value = false
  }
}

function certDomain(certID: number): string {
  return certificates.value.find((item) => item.id === certID)?.primary_domain || t('credentials.certHash', { id: certID })
}

function defaultConfig(def?: ProviderDef): Record<string, string> {
  const config: Record<string, string> = {}
  for (const field of def?.fields || []) {
    if (field.default) config[field.key] = field.default
    else if (field.type === 'select' && field.options?.length) config[field.key] = field.options[0].value
    else config[field.key] = ''
  }
  return config
}

function openCreate(): void {
  editingId.value = 0
  const def = typeOptions.value[0]
  form.name = ''
  form.type = def?.value || ''
  form.config = defaultConfig(def)
  form.remark = ''
  dialogVisible.value = true
}

function openEdit(item: CredentialItem): void {
  editingId.value = item.id
  form.name = item.name
  form.type = (isDNS.value ? item.provider : item.type) || ''
  form.config = { ...(item.config || {}) }
  // 补齐新版本新增的字段
  for (const [key, value] of Object.entries(defaultConfig(activeDef.value))) {
    if (form.config[key] === undefined) form.config[key] = value
  }
  form.remark = item.remark || ''
  dialogVisible.value = true
}

function onTypeChange(_value: string): void {
  form.config = defaultConfig(activeDef.value)
  verifyResult.value = null
  if (!isDNS.value && form.type === 'ssh') {
    form.config.port = '22'
  }
}

async function save(): Promise<void> {
  if (!form.name.trim()) {
    ElMessage.warning(t('credentials.nameRequired'))
    return
  }
  if (!form.type) {
    ElMessage.warning(t('credentials.typeRequired'))
    return
  }
  const payload: Record<string, unknown> = { name: form.name.trim(), config: form.config, remark: form.remark }
  if (isDNS.value) payload.provider = form.type
  else payload.type = form.type
  saving.value = true
  try {
    const base = isDNS.value ? '/dns-accounts' : '/deploy-accounts'
    const saved = await (editingId.value
        ? api.put(`${base}/${editingId.value}`, payload)
        : api.post(base, payload))
    if (saved === null) return
    ElMessage.success(t('credentials.saved'))
    dialogVisible.value = false
    load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function handleCommand(command: string, item: CredentialItem): Promise<void> {
  if (command === 'edit') {
    openEdit(item)
    return
  }
  try {
    await ElMessageBox.confirm(t('credentials.deleteConfirm', { name: item.name }), t('credentials.deleteTitle'), {
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    const base = isDNS.value ? '/dns-accounts' : '/deploy-accounts'
    const deleted = await api.delete(`${base}/${item.id}`)
    if (deleted === null) return
    ElMessage.success(t('credentials.deleted'))
    load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function isSelected(item: CredentialItem): boolean {
  return selectedIds.value.includes(item.id)
}

function toggleItem(item: CredentialItem, checked: boolean): void {
  if (checked) {
    if (!selectedIds.value.includes(item.id)) selectedIds.value.push(item.id)
  } else {
    selectedIds.value = selectedIds.value.filter((id) => id !== item.id)
  }
}

function toggleAll(value: string | number | boolean): void {
  selectedIds.value = value ? items.value.map((item) => item.id) : []
}

function clearSelection(): void {
  selectedIds.value = []
}

async function batchDelete(): Promise<void> {
  const ids = [...selectedIds.value]
  if (!ids.length) return
  try {
    await ElMessageBox.confirm(t('credentials.batchDeleteConfirm', { count: ids.length }), t('credentials.batchDeleteTitle'), {
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
    for (const id of ids) {
      try {
        await api.delete(`${isDNS.value ? '/dns-accounts' : '/deploy-accounts'}/${id}`)
      } catch (error: any) {
        failed++
        const name = items.value.find((item) => item.id === id)?.name || id
        ElMessage.error(`${name}: ${error.message}`)
      }
    }
  } finally {
    batchDeleting.value = false
  }
  if (failed) ElMessage.warning(t('credentials.batchDeleteFailed', { count: failed }))
  else ElMessage.success(t('credentials.batchDeleted', { count: ids.length }))
  await load()
}

onMounted(load)
onMounted(() => {
  void loadPluginIcons()
})
</script>

<style scoped>
.form-help {
  font-size: 12px;
  color: #94a3b8;
  line-height: 1.6;
  margin-top: 6px;
}

.cred-list {
  padding: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.cred-scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
}

.cred-list-head {
  flex: none;
}

.cred-footer {
  flex: none;
  padding: 12px 20px;
  border-top: 1px solid var(--kp-border);
  background: #ffffff;
}

.cred-empty-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 56px 20px;
  text-align: center;
}

.cred-head-name {
  display: flex;
  align-items: center;
  gap: 10px;
}

.cred-head-name :deep(.el-checkbox) {
  height: auto;
  margin-right: 0;
}

.cred-check {
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

.zone-hint {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 10px 12px;
  border: 1px solid #eef2f8;
  background: #f7f9fd;
  color: #64748b;
  font-size: 12px;
  line-height: 1.7;
}

.zone-hint-icon {
  flex: none;
  margin-top: 4px;
  color: #94a3b8;
  cursor: help;
}

.zone-list {
  max-height: 300px;
  margin-top: 12px;
  overflow-y: auto;
}

.zone-head,
.zone-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 104px 88px 44px 36px;
  gap: 8px;
  align-items: center;
}

.zone-head {
  position: sticky;
  top: 0;
  z-index: 1;
  padding: 0 4px 6px;
  border-bottom: 1px solid #eef1f6;
  background: #ffffff;
  color: #94a3b8;
  font-size: 12px;
}

.zone-empty {
  padding: 28px 0;
  color: #94a3b8;
  font-size: 12px;
  text-align: center;
}

.zone-row {
  padding: 6px 4px;
  border-bottom: 1px dashed #f1f5f9;
}

.zone-row.is-off {
  opacity: 0.55;
}

.zone-row > .el-select,
.zone-row > .el-input-number {
  width: 100%;
}

.zone-cell {
  min-width: 0;
}

.zone-row-end {
  display: contents;
}

.zone-error {
  margin-top: 4px;
  color: #dc2626;
  font-size: 12px;
  line-height: 1.4;
}

.zone-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}

.zone-actions-spacer {
  flex: 1 1 auto;
}

.zone-probe-input {
  flex: 0 1 220px;
}

@media (max-width: 560px) {
  .zone-head {
    display: none;
  }
  .zone-row {
    grid-template-columns: 96px 88px minmax(0, 1fr);
    grid-template-areas:
      'zone zone zone'
      'match priority end';
    row-gap: 8px;
    padding: 10px 4px;
  }
  .zone-cell {
    grid-area: zone;
  }
  .zone-row > .el-select {
    grid-area: match;
  }
  .zone-row > .el-input-number {
    grid-area: priority;
  }
  .zone-row-end {
    display: flex;
    grid-area: end;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
  }
  .zone-actions-spacer {
    display: none;
  }
  .zone-probe-input {
    flex: 1 1 160px;
  }
}

/* 最近部署记录抽屉：与任务执行日志共用同一种日志行样式。 */
.records-hint {
  margin-bottom: 8px;
  font-size: 12px;
}

.records-empty {
  text-align: center;
  padding: 40px 0;
}

.records-drawer-footer {
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
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

.cred-list-head,
.cred-row {
  display: grid;
  grid-template-columns: minmax(0, 2.2fr) minmax(0, 1.5fr) 170px 200px;
  align-items: center;
  gap: 16px;
}

.cred-list-head {
  padding: 12px 20px;
  background: #f8fafc;
  border-bottom: 1px solid var(--kp-border);
  color: var(--kp-muted);
  font-size: 12px;
  font-weight: 600;
}

.cred-head-actions {
  text-align: right;
}

.cred-row {
  padding: 16px 20px;
  border-bottom: 1px solid #f1f5f9;
  transition: background 0.15s ease;
}

.cred-row:last-child {
  border-bottom: 0;
}

.cred-row:hover {
  background: #f8fbff;
}

.cred-row.is-selected {
  background: #f5f9ff;
}

.cred-row.is-selected:hover {
  background: #eef5ff;
}

.cred-cell {
  min-width: 0;
}

.cred-name {
  display: flex;
  align-items: center;
  gap: 12px;
}

.cred-name-text {
  min-width: 0;
}

.cred-name-text .cred-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cred-remark {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cred-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
}

@media (min-width: 861px) {
  /* 凭据列表占满一屏剩余高度，页头保持固定。 */
  .cred-page > .page-head {
    flex: none;
  }
}

@media (max-width: 900px) {
  .cred-list-head {
    display: none;
  }

  .cred-row {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas:
      'name actions'
      'remark remark'
      'time time';
    row-gap: 8px;
  }

  .cred-name {
    grid-area: name;
  }

  .cred-remark {
    grid-area: remark;
  }

  .cred-time {
    grid-area: time;
  }

  .cred-actions {
    grid-area: actions;
  }
}
</style>

<style>
.provider-select-popper {
  --provider-select-width: min(480px, calc(100vw - 32px));
}

.provider-select-popper .el-select-dropdown {
  width: var(--provider-select-width);
  min-width: var(--provider-select-width) !important;
}

.provider-select-popper .el-select-dropdown__item {
  padding-right: 38px;
}

.provider-select-popper .provider-option {
  display: flex;
  align-items: center;
  gap: 16px;
  min-width: 0;
}

.provider-select-popper .provider-option-label {
  flex: 0 0 auto;
  max-width: 46%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
}

.provider-select-popper .provider-option-desc {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: right;
  font-size: 11px;
  color: var(--kp-muted);
}

.verify-check {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 6px;
  font-size: 12px;
}

.verify-check-label {
  flex: 0 0 auto;
  color: var(--kp-text);
}

.verify-check-message {
  flex: 1 1 auto;
  min-width: 0;
  word-break: break-word;
}
</style>
