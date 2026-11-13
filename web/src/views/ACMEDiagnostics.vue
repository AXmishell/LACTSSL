<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('acmeDiagnostics.title') }}</h1>
        <p>{{ t('acmeDiagnostics.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button type="warning" plain :icon="Delete" @click="openCleanup">{{ t('acmeDiagnostics.cleanup') }}</el-button>
        <el-button :icon="Back" @click="router.push('/acme-server')">{{ t('acmeDiagnostics.back') }}</el-button>
        <el-button :icon="Refresh" circle @click="refresh" />
      </div>
    </div>

    <div class="k-card" style="padding: 0">
      <el-tabs v-model="tab" @tab-change="onTabChange">
        <el-tab-pane :label="t('acmeDiagnostics.accounts')" name="accounts">
          <div class="filter-row">
            <el-select v-model="accountEndpoint" clearable :placeholder="t('acmeDiagnostics.allEndpoints')" style="width: 220px" @change="loadAccounts(1)">
              <el-option v-for="endpoint in endpoints" :key="endpoint.id" :value="endpoint.id" :label="endpoint.name" />
            </el-select>
            <span class="muted">{{ t('acmeDiagnostics.total', { count: accountTotal }) }}</span>
          </div>
          <el-table :data="accounts" v-loading="accountLoading" style="width: 100%">
            <el-table-column width="150" :label="t('acmeDiagnostics.endpoint')">
              <template #default="{ row }">{{ row.endpoint_name || `#${row.endpoint_id}` }}</template>
            </el-table-column>
            <el-table-column min-width="300" :label="t('acmeDiagnostics.accountKid')">
              <template #default="{ row }">
                <div class="diag-kid">
                  <code class="mono break-all">{{ row.kid }}</code>
                  <el-button text type="primary" size="small" :icon="CopyDocument" @click="copy(row.kid)" />
                </div>
              </template>
            </el-table-column>
            <el-table-column min-width="180" :label="t('acmeDiagnostics.contact')">
              <template #default="{ row }">
                <div v-for="item in row.contact || []" :key="item" class="muted">{{ item }}</div>
                <span v-if="!row.contact?.length" class="muted">{{ t('common.none') }}</span>
              </template>
            </el-table-column>
            <el-table-column width="100" :label="t('common.status')">
              <template #default="{ row }">
                <el-tag :type="row.status === 'valid' ? 'success' : 'info'" effect="light" round size="small">{{ row.status }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column width="170" :label="t('acmeDiagnostics.lastUsed')">
              <template #default="{ row }">{{ row.last_used_at ? formatDate(row.last_used_at) : t('common.none') }}</template>
            </el-table-column>
            <el-table-column width="170" :label="t('acmeDiagnostics.createdAt')">
              <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
            </el-table-column>
            <template #empty>
              <div style="padding: 36px 0; color: #94a3b8">{{ t('acmeDiagnostics.noAccounts') }}</div>
            </template>
          </el-table>
          <el-pagination
            v-if="accountTotal > pageSize"
            v-model:current-page="accountPage"
            :page-size="pageSize"
            :total="accountTotal"
            layout="prev, pager, next"
            style="margin-top: 16px; justify-content: flex-end"
            @current-change="loadAccounts"
          />
        </el-tab-pane>

        <el-tab-pane :label="t('acmeDiagnostics.orders')" name="orders">
          <div class="filter-row">
            <el-select v-model="orderEndpoint" clearable :placeholder="t('acmeDiagnostics.allEndpoints')" style="width: 220px" @change="loadOrders(1)">
              <el-option v-for="endpoint in endpoints" :key="endpoint.id" :value="endpoint.id" :label="endpoint.name" />
            </el-select>
            <el-select v-model="orderStatus" clearable :placeholder="t('acmeDiagnostics.allStatuses')" style="width: 160px" @change="loadOrders(1)">
              <el-option v-for="status in orderStatuses" :key="status" :value="status" :label="status" />
            </el-select>
            <span class="muted">{{ t('acmeDiagnostics.total', { count: orderTotal }) }}</span>
          </div>
          <el-table :data="orders" v-loading="orderLoading" style="width: 100%">
            <el-table-column width="140" :label="t('acmeDiagnostics.endpoint')">
              <template #default="{ row }">{{ row.endpoint_name || `#${row.endpoint_id}` }}</template>
            </el-table-column>
            <el-table-column min-width="240" :label="t('acmeDiagnostics.identifiers')">
              <template #default="{ row }">
                <div class="flex wrap gap-6">
                  <span v-for="identifier in row.identifiers || []" :key="identifier" class="tag-soft tag-gray">{{ identifier }}</span>
                </div>
              </template>
            </el-table-column>
            <el-table-column width="120" :label="t('common.status')">
              <template #default="{ row }">
                <el-tag :type="orderTag(row.status)" effect="light" round size="small">{{ row.status }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column width="110" :label="t('acmeDiagnostics.certificate')">
              <template #default="{ row }">
                <el-tag v-if="row.certificate_state" :type="row.certificate_state === 'revoked' ? 'danger' : 'success'" effect="light" round size="small">
                  {{ row.certificate_state }}
                </el-tag>
                <span v-else class="muted">{{ t('common.none') }}</span>
              </template>
            </el-table-column>
            <el-table-column width="170" :label="t('acmeDiagnostics.createdAt')">
              <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
            </el-table-column>
            <el-table-column width="170" :label="t('acmeDiagnostics.expiresAt')">
              <template #default="{ row }">{{ row.expires_at ? formatDate(row.expires_at) : t('common.none') }}</template>
            </el-table-column>
            <el-table-column width="100" :label="t('common.actions')" fixed="right">
              <template #default="{ row }">
                <el-button text type="primary" size="small" @click="openDetail(row)">{{ t('common.details') }}</el-button>
              </template>
            </el-table-column>
            <template #empty>
              <div style="padding: 36px 0; color: #94a3b8">{{ t('acmeDiagnostics.noOrders') }}</div>
            </template>
          </el-table>
          <el-pagination
            v-if="orderTotal > pageSize"
            v-model:current-page="orderPage"
            :page-size="pageSize"
            :total="orderTotal"
            layout="prev, pager, next"
            style="margin-top: 16px; justify-content: flex-end"
            @current-change="loadOrders"
          />
        </el-tab-pane>
      </el-tabs>
    </div>

    <el-drawer v-model="detailVisible" :title="t('acmeDiagnostics.orderDetail')" size="780px">
      <div v-if="detail" v-loading="detailLoading">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item :label="t('acmeDiagnostics.endpoint')">{{ detail.order.endpoint_name || `#${detail.order.endpoint_id}` }}</el-descriptions-item>
          <el-descriptions-item :label="t('common.status')">
            <el-tag :type="orderTag(detail.order.status)" effect="light" round size="small">{{ detail.order.status }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="t('acmeDiagnostics.accountKid')" :span="2">
            <div class="diag-kid">
              <code class="mono break-all">{{ detail.order.account_kid || `#${detail.order.account_id}` }}</code>
              <el-button v-if="detail.order.account_kid" text type="primary" size="small" :icon="CopyDocument" @click="copy(detail.order.account_kid)" />
            </div>
          </el-descriptions-item>
          <el-descriptions-item :label="t('acmeDiagnostics.identifiers')" :span="2">
            <div class="flex wrap gap-6">
              <span v-for="identifier in detail.order.identifiers || []" :key="identifier" class="tag-soft tag-gray">{{ identifier }}</span>
            </div>
          </el-descriptions-item>
          <el-descriptions-item v-if="detail.order.replaces" :label="t('acmeDiagnostics.replaces')" :span="2">
            <code class="mono break-all">{{ detail.order.replaces }}</code>
          </el-descriptions-item>
          <el-descriptions-item :label="t('acmeDiagnostics.createdAt')">{{ formatDate(detail.order.created_at) }}</el-descriptions-item>
          <el-descriptions-item :label="t('acmeDiagnostics.expiresAt')">{{ detail.order.expires_at ? formatDate(detail.order.expires_at) : t('common.none') }}</el-descriptions-item>
          <el-descriptions-item v-if="detail.order.error" :label="t('acmeDiagnostics.error')" :span="2">
            <span class="diag-error">{{ detail.order.error }}</span>
          </el-descriptions-item>
        </el-descriptions>

        <h3 style="margin: 22px 0 10px">{{ t('acmeDiagnostics.authorizations') }}</h3>
        <div v-for="authz in detail.authorizations || []" :key="authz.public_id" class="k-card" style="padding: 14px 16px; margin-bottom: 12px">
          <div class="flex-between">
            <div>
              <strong class="mono">{{ authz.identifier }}</strong>
              <span v-if="authz.wildcard" class="tag-soft tag-blue" style="margin-left: 8px">*</span>
            </div>
            <el-tag :type="authz.status === 'valid' ? 'success' : authz.status === 'invalid' ? 'danger' : 'info'" effect="light" round size="small">{{ authz.status }}</el-tag>
          </div>
          <div class="muted" style="font-size: 12px; margin-top: 4px">{{ t('acmeDiagnostics.authzExpires', { time: authz.expires_at ? formatDate(authz.expires_at) : t('common.none') }) }}</div>
          <el-table :data="authz.challenges || []" size="small" style="width: 100%; margin-top: 10px">
            <el-table-column width="110" :label="t('acmeDiagnostics.challengeType')" prop="type" />
            <el-table-column width="110" :label="t('common.status')">
              <template #default="{ row }">
                <el-tag :type="row.status === 'valid' ? 'success' : row.status === 'invalid' ? 'danger' : 'info'" effect="light" round size="small">{{ row.status }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column min-width="220" :label="t('acmeDiagnostics.challengeError')">
              <template #default="{ row }">
                <span v-if="row.error" class="diag-error">{{ row.error }}</span>
                <span v-else class="muted">{{ t('common.none') }}</span>
              </template>
            </el-table-column>
            <el-table-column width="170" :label="t('acmeDiagnostics.validatedAt')">
              <template #default="{ row }">{{ row.validated_at ? formatDate(row.validated_at) : t('common.none') }}</template>
            </el-table-column>
          </el-table>
        </div>

        <template v-if="detail.certificate">
          <h3 style="margin: 22px 0 10px">{{ t('acmeDiagnostics.certificate') }}</h3>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item :label="t('acmeDiagnostics.serial')"><span class="mono break-all">{{ detail.certificate.serial }}</span></el-descriptions-item>
            <el-descriptions-item :label="t('common.status')">
              <el-tag :type="detail.certificate.status === 'revoked' ? 'danger' : 'success'" effect="light" round size="small">{{ detail.certificate.status }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="t('acmeDiagnostics.notBefore')">{{ formatDate(detail.certificate.not_before) }}</el-descriptions-item>
            <el-descriptions-item :label="t('acmeDiagnostics.notAfter')">{{ formatDate(detail.certificate.not_after) }}</el-descriptions-item>
          </el-descriptions>
        </template>
      </div>
    </el-drawer>

    <!-- 清理诊断记录 -->
    <el-dialog v-model="cleanupVisible" :title="t('acmeDiagnostics.cleanupTitle')" width="680px">
      <el-alert :title="t('acmeDiagnostics.cleanupIntro')" type="warning" :closable="false" show-icon />
      <el-form label-position="top" class="cleanup-form">
        <el-form-item :label="t('acmeDiagnostics.cleanupTarget')">
          <el-radio-group v-model="cleanupForm.target">
            <el-radio-button value="orders">{{ t('acmeDiagnostics.cleanupTargetOrders') }}</el-radio-button>
            <el-radio-button value="accounts">{{ t('acmeDiagnostics.cleanupTargetAccounts') }}</el-radio-button>
            <el-radio-button value="all">{{ t('acmeDiagnostics.cleanupTargetAll') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="t('acmeDiagnostics.cleanupScope')">
          <el-radio-group v-model="cleanupForm.scope">
            <el-radio-button value="stale">{{ t('acmeDiagnostics.cleanupScopeStale') }}</el-radio-button>
            <el-radio-button value="all">{{ t('acmeDiagnostics.cleanupScopeAll') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <div class="cleanup-grid">
          <el-form-item :label="t('acmeDiagnostics.cleanupEndpoint')">
            <el-select v-model="cleanupForm.endpoint_id" clearable :placeholder="t('acmeDiagnostics.cleanupAnyEndpoint')">
              <el-option v-for="endpoint in endpoints" :key="endpoint.id" :value="endpoint.id" :label="endpoint.name" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="cleanupHasOrders" :label="t('acmeDiagnostics.cleanupOlderThanDays')">
            <el-input-number v-model="cleanupForm.older_than_days" :min="0" :max="36500" controls-position="right" />
          </el-form-item>
          <el-form-item v-if="cleanupHasAccounts" :label="t('acmeDiagnostics.cleanupInactiveDays')">
            <el-input-number v-model="cleanupForm.inactive_days" :min="0" :max="36500" controls-position="right" />
          </el-form-item>
        </div>
        <div class="cleanup-note">{{ cleanupScopeHint }}</div>
        <div class="cleanup-note">{{ t('acmeDiagnostics.cleanupDaysHint') }}</div>
      </el-form>
      <el-alert
        class="cleanup-preview"
        :title="cleanupPreviewLoading ? t('acmeDiagnostics.cleanupPreviewLoading') : t('acmeDiagnostics.cleanupPreview', { orders: cleanupPreview.orders, accounts: cleanupPreview.accounts })"
        :type="cleanupPreview.count ? 'error' : 'info'"
        :closable="false"
        show-icon
      />
      <template #footer>
        <el-button @click="cleanupVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="danger" :loading="cleaning" :disabled="cleanupPreview.count === 0" @click="submitCleanup">{{ t('acmeDiagnostics.cleanupStart') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { Back, CopyDocument, Delete, Refresh } from '@element-plus/icons-vue'
import { api, copyText } from '../api'
import { withSecondaryVerify } from '../secondaryVerify'
import { formatDate } from '../utils'
import type { ACMEAccount, ACMEEndpoint, ACMEOrder, ACMEOrderDetail } from '../types'

const { t } = useI18n()
const router = useRouter()
const tab = ref<'accounts' | 'orders'>('accounts')
const endpoints = ref<ACMEEndpoint[]>([])
const pageSize = 50

const accountEndpoint = ref<number | undefined>()
const accountPage = ref(1)
const accountTotal = ref(0)
const accounts = ref<ACMEAccount[]>([])
const accountLoading = ref(false)

const orderEndpoint = ref<number | undefined>()
const orderStatus = ref('')
const orderPage = ref(1)
const orderTotal = ref(0)
const orders = ref<ACMEOrder[]>([])
const orderLoading = ref(false)
const orderStatuses = ['pending', 'ready', 'processing', 'valid', 'invalid']

const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<ACMEOrderDetail | null>(null)

interface CleanupForm {
  target: 'orders' | 'accounts' | 'all'
  scope: 'stale' | 'all'
  endpoint_id?: number
  older_than_days: number
  inactive_days: number
}

interface CleanupPreview {
  orders: number
  accounts: number
  count: number
}

const cleanupVisible = ref(false)
const cleaning = ref(false)
const cleanupPreviewLoading = ref(false)
const cleanupPreview = ref<CleanupPreview>({ orders: 0, accounts: 0, count: 0 })
const cleanupForm = reactive<CleanupForm>({
  target: 'orders',
  scope: 'stale',
  endpoint_id: undefined,
  older_than_days: 0,
  inactive_days: 90
})
const cleanupHasOrders = computed(() => cleanupForm.target !== 'accounts')
const cleanupHasAccounts = computed(() => cleanupForm.target !== 'orders')
const cleanupScopeHint = computed(() => {
  if (cleanupForm.scope === 'all') return t('acmeDiagnostics.cleanupScopeAllHint')
  if (cleanupForm.target === 'accounts') return t('acmeDiagnostics.cleanupScopeStaleAccounts')
  if (cleanupForm.target === 'all') return t('acmeDiagnostics.cleanupScopeStaleAll')
  return t('acmeDiagnostics.cleanupScopeStaleOrders')
})
let cleanupPreviewTimer: number | undefined
let cleanupPreviewSeq = 0

async function loadEndpoints(): Promise<void> {
  try {
    endpoints.value = (await api.get<ACMEEndpoint[]>('/acme-endpoints')) || []
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function loadAccounts(page = accountPage.value): Promise<void> {
  accountPage.value = page
  accountLoading.value = true
  try {
    const query = new URLSearchParams({ limit: String(pageSize), offset: String((page - 1) * pageSize) })
    if (accountEndpoint.value) query.set('endpoint_id', String(accountEndpoint.value))
    const data = await api.get<{ items: ACMEAccount[]; total: number }>(`/acme-accounts?${query.toString()}`)
    accounts.value = data?.items || []
    accountTotal.value = data?.total || 0
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    accountLoading.value = false
  }
}

async function loadOrders(page = orderPage.value): Promise<void> {
  orderPage.value = page
  orderLoading.value = true
  try {
    const query = new URLSearchParams({ limit: String(pageSize), offset: String((page - 1) * pageSize) })
    if (orderEndpoint.value) query.set('endpoint_id', String(orderEndpoint.value))
    if (orderStatus.value) query.set('status', orderStatus.value)
    const data = await api.get<{ items: ACMEOrder[]; total: number }>(`/acme-orders?${query.toString()}`)
    orders.value = data?.items || []
    orderTotal.value = data?.total || 0
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    orderLoading.value = false
  }
}

async function openDetail(row: ACMEOrder): Promise<void> {
  detailVisible.value = true
  detailLoading.value = true
  detail.value = null
  try {
    detail.value = await api.get<ACMEOrderDetail>(`/acme-orders/${row.id}`)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    detailLoading.value = false
  }
}

function orderTag(status: string): 'success' | 'warning' | 'danger' | 'info' {
  if (status === 'valid') return 'success'
  if (status === 'invalid') return 'danger'
  if (status === 'ready' || status === 'processing') return 'warning'
  return 'info'
}

async function copy(value: string): Promise<void> {
  if (await copyText(value)) ElMessage.success(t('common.copied'))
  else ElMessage.warning(t('common.copyFailedManually'))
}

function cleanupPayload(): Record<string, unknown> {
  return {
    target: cleanupForm.target,
    scope: cleanupForm.scope,
    endpoint_id: cleanupForm.endpoint_id || 0,
    older_than_days: cleanupForm.older_than_days || 0,
    inactive_days: cleanupForm.inactive_days || 0
  }
}

async function previewCleanup(): Promise<void> {
  if (!cleanupVisible.value) return
  const seq = ++cleanupPreviewSeq
  cleanupPreviewLoading.value = true
  try {
    const data = await api.post<CleanupPreview>('/acme-diagnostics/cleanup/preview', cleanupPayload())
    if (seq !== cleanupPreviewSeq) return
    cleanupPreview.value = {
      orders: data?.orders || 0,
      accounts: data?.accounts || 0,
      count: data?.count || 0
    }
  } catch (error: any) {
    if (seq !== cleanupPreviewSeq) return
    cleanupPreview.value = { orders: 0, accounts: 0, count: 0 }
    ElMessage.error(error.message || t('acmeDiagnostics.cleanupFailed'))
  } finally {
    if (seq === cleanupPreviewSeq) cleanupPreviewLoading.value = false
  }
}

function scheduleCleanupPreview(): void {
  if (!cleanupVisible.value) return
  window.clearTimeout(cleanupPreviewTimer)
  cleanupPreviewTimer = window.setTimeout(() => { void previewCleanup() }, 250)
}

function openCleanup(): void {
  cleanupForm.target = 'orders'
  cleanupForm.scope = 'stale'
  cleanupForm.endpoint_id = undefined
  cleanupForm.older_than_days = 0
  cleanupForm.inactive_days = 90
  cleanupPreview.value = { orders: 0, accounts: 0, count: 0 }
  cleanupVisible.value = true
  void previewCleanup()
}

async function submitCleanup(): Promise<void> {
  if (!cleanupPreview.value.count) {
    ElMessage.info(t('acmeDiagnostics.cleanupEmpty'))
    return
  }
  cleaning.value = true
  try {
    const data = await withSecondaryVerify<{ orders: number; accounts: number }>('acme_write', (headers) =>
      api.post<{ orders: number; accounts: number }>('/acme-diagnostics/cleanup', cleanupPayload(), headers)
    )
    if (data === null) return
    ElMessage.success(t('acmeDiagnostics.cleanupDone', { orders: data?.orders || 0, accounts: data?.accounts || 0 }))
    cleanupVisible.value = false
    detailVisible.value = false
    await Promise.all([loadAccounts(1), loadOrders(1)])
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    cleaning.value = false
  }
}

watch(cleanupForm, scheduleCleanupPreview, { deep: true })

async function refresh(): Promise<void> {
  await loadEndpoints()
  if (tab.value === 'accounts') await loadAccounts()
  else await loadOrders()
}

async function onTabChange(): Promise<void> {
  if (tab.value === 'accounts') await loadAccounts()
  else await loadOrders()
}

onMounted(async () => {
  await loadEndpoints()
  await loadAccounts()
})
</script>

<style scoped>
/* 收紧「账户 / 订单」两个标签之间的间距，同时保留首个标签与内容的左对齐 */
:deep(.el-tabs__item) {
  padding: 0 12px;
}
:deep(#tab-accounts) {
  padding-left: 20px;
}
/* 卡片本身不再留边，筛选区自行保留内边距，让表格铺满卡片 */
.filter-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 8px 16px 12px;
}
.diag-kid {
  display: flex;
  align-items: flex-start;
  gap: 4px;
}
.diag-kid code {
  flex: 1;
  font-size: 12px;
  color: #475569;
}
.diag-error {
  color: #dc2626;
  font-size: 12px;
  word-break: break-all;
}
.cleanup-form {
  margin-top: 18px;
}
.cleanup-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 18px;
}
.cleanup-grid :deep(.el-input-number),
.cleanup-grid :deep(.el-select) {
  width: 100%;
}
.cleanup-note {
  margin: -4px 0 10px;
  color: #94a3b8;
  font-size: 12.5px;
  line-height: 1.6;
}
.cleanup-preview {
  margin-top: 6px;
}
</style>
