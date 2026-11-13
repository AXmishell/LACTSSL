<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h1>{{ t('privateCA.title') }}</h1>
        <p>{{ t('privateCA.subtitle') }}</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Upload" @click="openImport">{{ t('privateCA.import') }}</el-button>
        <el-button type="primary" :icon="Plus" @click="openGenerate()">{{ t('privateCA.generateCA') }}</el-button>
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <el-alert
      :title="t('privateCA.alert')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 18px"
    />

    <div class="k-card" style="padding: 0; overflow: hidden">
      <el-table
        ref="tableRef"
        :data="treeItems"
        v-loading="loading"
        row-key="id"
        default-expand-all
        :tree-props="{ children: 'children' }"
        style="width: 100%"
        @selection-change="onSelectionChange"
      >
        <el-table-column type="selection" width="48" fixed="left" :reserve-selection="true" />
        <el-table-column width="150" :label="t('privateCA.ca')">
          <template #default="{ row }">
            <div class="ca-title">
              {{ row.name }}
              <el-tag :type="row.ca_type === 'intermediate' ? 'warning' : 'success'" effect="light" round size="small">
                {{ caTypeLabel(row) }}
              </el-tag>
            </div>
            <div class="ca-desc">{{ row.common_name }}</div>
            <div v-if="row.organization || row.country" class="ca-desc">
              {{ [row.organization, row.country].filter(Boolean).join(' · ') }}
            </div>
            <div class="ca-desc">
              {{ t('privateCA.pathLenLabel', { value: row.path_len < 0 ? t('privateCA.unlimited') : row.path_len }) }}
            </div>
          </template>
        </el-table-column>
        <el-table-column width="140" :label="t('privateCA.keyType')">
          <template #default="{ row }">{{ keyTypeLabel(row.key_type) }}</template>
        </el-table-column>
        <el-table-column width="110" :label="t('privateCA.source')">
          <template #default="{ row }">
            <el-tag :type="row.source === 'imported' ? 'info' : 'success'" effect="light" round size="small">
              {{ row.source === 'imported' ? t('privateCA.sourceImported') : t('privateCA.sourceGenerated') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column width="190" :label="t('privateCA.expiresAt')">
          <template #default="{ row }">
            <div class="ca-title">{{ formatDate(row.not_after) }}</div>
            <div class="ca-desc">{{ t('privateCA.daysLeft', { days: daysLeft(row.not_after) }) }}</div>
          </template>
        </el-table-column>
        <el-table-column min-width="180" :label="t('privateCA.serial')">
          <template #default="{ row }">
            <span class="mono break-all">{{ row.serial }}</span>
          </template>
        </el-table-column>
        <el-table-column width="100" align="center" header-align="center" class-name="ca-actions-cell" :label="t('privateCA.actions')" fixed="right">
          <template #default="{ row }">
            <el-dropdown trigger="click" @command="(kind: string) => downloadCA(row, kind)">
              <el-button text type="primary" size="small">
                {{ t('privateCA.download') }}<el-icon class="el-icon--right"><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="cert">{{ t('privateCA.downloadCert') }}</el-dropdown-item>
                  <el-dropdown-item command="chain">{{ t('privateCA.downloadChain') }}</el-dropdown-item>
                  <el-dropdown-item command="key">{{ t('privateCA.downloadKey') }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
            <el-button text type="danger" size="small" @click="remove(row)">{{ t('privateCA.delete') }}</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div style="padding: 40px 0; color: #94a3b8">
            <el-icon size="30"><Lock /></el-icon>
            <p>{{ t('privateCA.empty') }}</p>
          </div>
        </template>
      </el-table>
    </div>

    <div v-if="selectedCAs.length" class="batch-bar">
      <div class="batch-actions">
        <el-button size="small" type="danger" plain :icon="Delete" :loading="batchDeleting" @click="batchDelete">
          {{ t('privateCA.batchDelete') }}
        </el-button>
        <el-button size="small" text :icon="Close" @click="clearSelection">{{ t('privateCA.clearSelection') }}</el-button>
      </div>
      <span class="batch-count">{{ t('privateCA.selectedCount', { count: selectedCAs.length }) }}</span>
    </div>

    <!-- 生成私有 CA -->
    <el-dialog v-model="generateVisible" :title="t('privateCA.generateTitle')" width="760px" destroy-on-close>
      <el-form :model="generateForm" label-position="top">
        <el-form-item :label="t('privateCA.caType')">
          <el-radio-group v-model="generateForm.ca_type">
            <el-radio-button value="root">{{ t('privateCA.rootCA') }}</el-radio-button>
            <el-radio-button value="intermediate">{{ t('privateCA.intermediateCA') }}</el-radio-button>
          </el-radio-group>
          <div class="form-help" style="margin-left: 12px">{{ t('privateCA.caTypeHelp') }}</div>
        </el-form-item>
        <el-form-item v-if="generateForm.ca_type === 'intermediate'" :label="t('privateCA.parentCA')" required>
          <el-select v-model="generateForm.parent_id" style="width: 100%">
            <el-option
              v-for="ca in items"
              :key="ca.id"
              :value="ca.id"
              :disabled="!canIssueCA(ca)"
              :label="`${ca.name} (${ca.common_name})`"
            />
          </el-select>
          <div class="form-help">{{ t('privateCA.parentCAHelp') }}</div>
        </el-form-item>
        <el-form-item :label="t('privateCA.name')" required>
          <el-input v-model="generateForm.name" :placeholder="t('privateCA.namePlaceholder')" maxlength="100" />
        </el-form-item>
        <el-form-item :label="t('privateCA.commonName')">
          <el-input v-model="generateForm.common_name" :placeholder="t('privateCA.commonNamePlaceholder')" maxlength="64" />
          <div class="form-help">{{ t('privateCA.commonNameHelp') }}</div>
        </el-form-item>
        <div class="grid grid-2" style="gap: 0 18px">
          <el-form-item :label="t('privateCA.organization')">
            <el-input v-model="generateForm.organization" :placeholder="t('privateCA.organizationPlaceholder')" maxlength="64" />
          </el-form-item>
          <el-form-item :label="t('privateCA.country')">
            <el-input v-model="generateForm.country" :placeholder="t('privateCA.countryPlaceholder')" maxlength="2" />
          </el-form-item>
          <el-form-item :label="t('privateCA.keyType')">
            <el-select v-model="generateForm.key_type" style="width: 100%">
              <el-option v-for="item in keyTypes" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('privateCA.validDays')">
            <el-input-number v-model="generateForm.valid_days" :min="1" :max="36500" style="width: 100%" />
          </el-form-item>
        </div>

        <el-collapse v-model="advancedOpen" style="margin-top: 6px">
          <el-collapse-item name="subject" :title="t('privateCA.subjectAdvanced')">
            <div class="grid grid-2" style="gap: 0 18px">
              <el-form-item :label="t('privateCA.state')">
                <el-input v-model="generateForm.state" :placeholder="t('privateCA.statePlaceholder')" maxlength="64" />
              </el-form-item>
              <el-form-item :label="t('privateCA.locality')">
                <el-input v-model="generateForm.locality" :placeholder="t('privateCA.localityPlaceholder')" maxlength="64" />
              </el-form-item>
              <el-form-item :label="t('privateCA.organizationalUnit')">
                <el-input v-model="generateForm.organizational_unit" :placeholder="t('privateCA.organizationalUnitPlaceholder')" maxlength="64" />
              </el-form-item>
              <el-form-item :label="t('privateCA.email')">
                <el-input v-model="generateForm.email" :placeholder="t('privateCA.emailPlaceholder')" maxlength="254" />
              </el-form-item>
            </div>
            <el-form-item :label="t('privateCA.serialNumber')">
              <el-input v-model="generateForm.serial_number" :placeholder="t('privateCA.serialNumberPlaceholder')" maxlength="40" />
              <div class="form-help">{{ t('privateCA.serialNumberHelp') }}</div>
            </el-form-item>
          </el-collapse-item>

          <el-collapse-item name="extensions" :title="t('privateCA.extensionsAdvanced')">
            <div class="grid grid-2" style="gap: 0 18px">
              <el-form-item :label="t('privateCA.pathLen')">
                <el-input-number v-model="generateForm.path_len" :min="-1" :max="10" style="width: 100%" />
                <div class="form-help">{{ t('privateCA.pathLenHelp') }}</div>
              </el-form-item>
            </div>
            <el-form-item :label="t('privateCA.keyUsage')">
              <el-checkbox-group v-model="generateForm.key_usage">
                <el-checkbox value="digital_signature">{{ t('privateCA.kuDigitalSignature') }}</el-checkbox>
                <el-checkbox value="content_commitment">{{ t('privateCA.kuContentCommitment') }}</el-checkbox>
                <el-checkbox value="key_encipherment">{{ t('privateCA.kuKeyEncipherment') }}</el-checkbox>
                <el-checkbox value="key_cert_sign">{{ t('privateCA.kuKeyCertSign') }}</el-checkbox>
                <el-checkbox value="crl_sign">{{ t('privateCA.kuCRLSign') }}</el-checkbox>
              </el-checkbox-group>
              <div class="form-help">{{ t('privateCA.keyUsageHelp') }}</div>
            </el-form-item>
            <el-form-item :label="t('privateCA.extKeyUsage')">
              <el-checkbox-group v-model="generateForm.ext_key_usage">
                <el-checkbox value="server_auth">{{ t('privateCA.ekuServerAuth') }}</el-checkbox>
                <el-checkbox value="client_auth">{{ t('privateCA.ekuClientAuth') }}</el-checkbox>
                <el-checkbox value="code_signing">{{ t('privateCA.ekuCodeSigning') }}</el-checkbox>
                <el-checkbox value="email_protection">{{ t('privateCA.ekuEmailProtection') }}</el-checkbox>
                <el-checkbox value="time_stamping">{{ t('privateCA.ekuTimeStamping') }}</el-checkbox>
                <el-checkbox value="ocsp_signing">{{ t('privateCA.ekuOCSPSigning') }}</el-checkbox>
              </el-checkbox-group>
              <div class="form-help">{{ t('privateCA.extKeyUsageHelp') }}</div>
            </el-form-item>
            <div class="grid grid-2" style="gap: 0 18px">
              <el-form-item :label="t('privateCA.sanDNS')">
                <el-input v-model="generateForm.dns_names" type="textarea" :rows="3" :placeholder="t('privateCA.listPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('privateCA.sanIP')">
                <el-input v-model="generateForm.ip_addresses" type="textarea" :rows="3" :placeholder="t('privateCA.ipListPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('privateCA.permittedDNS')">
                <el-input v-model="generateForm.permitted_dns" type="textarea" :rows="3" :placeholder="t('privateCA.dnsConstraintPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('privateCA.excludedDNS')">
                <el-input v-model="generateForm.excluded_dns" type="textarea" :rows="3" :placeholder="t('privateCA.dnsConstraintPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('privateCA.permittedIPs')">
                <el-input v-model="generateForm.permitted_ips" type="textarea" :rows="3" :placeholder="t('privateCA.cidrPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('privateCA.excludedIPs')">
                <el-input v-model="generateForm.excluded_ips" type="textarea" :rows="3" :placeholder="t('privateCA.cidrPlaceholder')" />
              </el-form-item>
            </div>
            <div class="form-help">{{ t('privateCA.nameConstraintsHelp') }}</div>
          </el-collapse-item>

          <el-collapse-item name="infra" :title="t('privateCA.infrastructureAdvanced')">
            <div class="grid grid-2" style="gap: 0 18px">
              <el-form-item :label="t('privateCA.crlURLs')">
                <el-input v-model="generateForm.crl_urls" type="textarea" :rows="3" :placeholder="t('privateCA.urlListPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('privateCA.ocspURLs')">
                <el-input v-model="generateForm.ocsp_urls" type="textarea" :rows="3" :placeholder="t('privateCA.urlListPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('privateCA.issuerURL')">
                <el-input v-model="generateForm.issuer_url" :placeholder="t('privateCA.urlPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('privateCA.cpsURL')">
                <el-input v-model="generateForm.cps_url" :placeholder="t('privateCA.cpsPlaceholder')" />
              </el-form-item>
            </div>
            <el-form-item :label="t('privateCA.policyOIDs')">
              <el-input v-model="generateForm.policy_oids" type="textarea" :rows="2" :placeholder="t('privateCA.policyOIDsPlaceholder')" />
              <div class="form-help">{{ t('privateCA.policyOIDsHelp') }}</div>
            </el-form-item>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button @click="generateVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="submitGenerate">{{ t('privateCA.generate') }}</el-button>
      </template>
    </el-dialog>

    <!-- 导入私有 CA -->
    <el-dialog v-model="importVisible" :title="t('privateCA.importTitle')" width="640px" destroy-on-close>
      <el-alert :title="t('privateCA.importHelp')" type="info" :closable="false" show-icon style="margin-bottom: 18px" />
      <el-form :model="importForm" label-position="top">
        <el-form-item :label="t('privateCA.name')" required>
          <el-input v-model="importForm.name" :placeholder="t('privateCA.namePlaceholder')" maxlength="100" />
        </el-form-item>
        <el-form-item :label="t('privateCA.importParentCA')">
          <el-select v-model="importForm.parent_id" style="width: 100%">
            <el-option :value="0" :label="t('privateCA.importParentNone')" />
            <el-option
              v-for="ca in items"
              :key="ca.id"
              :value="ca.id"
              :disabled="!canIssueCA(ca)"
              :label="`${ca.name} (${ca.common_name})`"
            />
          </el-select>
          <div class="form-help">{{ t('privateCA.importParentHelp') }}</div>
        </el-form-item>
        <el-form-item :label="t('privateCA.certPEM')" required>
          <el-input
            v-model="importForm.cert_pem"
            type="textarea"
            :rows="7"
            :placeholder="t('privateCA.certPEMPlaceholder')"
            class="mono"
          />
        </el-form-item>
        <el-form-item :label="t('privateCA.keyPEM')" required>
          <el-input
            v-model="importForm.key_pem"
            type="textarea"
            :rows="7"
            :placeholder="t('privateCA.keyPEMPlaceholder')"
            class="mono"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="importVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="submitImport">{{ t('privateCA.import') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, Close, Delete, Plus, Refresh, Upload } from '@element-plus/icons-vue'
import { api, downloadFile } from '../api'
import { requestSecondaryVerification, secondaryVerifyHeader } from '../secondaryVerify'
import { loadMeta } from '../store'
import { daysLeft, formatDate, splitLines } from '../utils'
import type { MessageResult, PrivateCA } from '../types'

interface GenerateForm {
  ca_type: 'root' | 'intermediate'
  name: string
  common_name: string
  organization: string
  organizational_unit: string
  country: string
  state: string
  locality: string
  email: string
  serial_number: string
  key_type: string
  valid_days: number
  parent_id: number
  path_len: number
  key_usage: string[]
  ext_key_usage: string[]
  dns_names: string
  ip_addresses: string
  permitted_dns: string
  excluded_dns: string
  permitted_ips: string
  excluded_ips: string
  crl_urls: string
  ocsp_urls: string
  issuer_url: string
  policy_oids: string
  cps_url: string
}

const { t } = useI18n()
const items = ref<PrivateCA[]>([])
const loading = ref(false)
const saving = ref(false)
const tableRef = ref<{ clearSelection: () => void } | null>(null)
const selectedCAs = ref<PrivateCA[]>([])
const batchDeleting = ref(false)
const generateVisible = ref(false)
const importVisible = ref(false)
const advancedOpen = ref<string[]>([])

interface PrivateCATree extends PrivateCA {
  children?: PrivateCATree[]
}

const treeItems = computed<PrivateCATree[]>(() => {
  const nodes = new Map<number, PrivateCATree>()
  for (const item of items.value) {
    nodes.set(item.id, { ...item })
  }
  const roots: PrivateCATree[] = []
  for (const item of items.value) {
    const node = nodes.get(item.id)
    if (!node) continue
    const parent = item.parent_id ? nodes.get(item.parent_id) : undefined
    if (parent) {
      parent.children = parent.children || []
      parent.children.push(node)
    } else {
      roots.push(node)
    }
  }
  return roots
})

const keyTypes = [
  { value: 'ecdsa', label: 'ECDSA P-256' },
  { value: 'ecdsa384', label: 'ECDSA P-384' },
  { value: 'rsa2048', label: 'RSA 2048' },
  { value: 'rsa4096', label: 'RSA 4096' }
]

function defaultGenerateForm(): GenerateForm {
  return {
    ca_type: 'root',
    name: '',
    common_name: '',
    organization: '',
    organizational_unit: '',
    country: '',
    state: '',
    locality: '',
    email: '',
    serial_number: '',
    key_type: 'ecdsa',
    valid_days: 3650,
    parent_id: 0,
    path_len: 1,
    key_usage: ['digital_signature', 'key_cert_sign', 'crl_sign'],
    ext_key_usage: [],
    dns_names: '',
    ip_addresses: '',
    permitted_dns: '',
    excluded_dns: '',
    permitted_ips: '',
    excluded_ips: '',
    crl_urls: '',
    ocsp_urls: '',
    issuer_url: '',
    policy_oids: '',
    cps_url: ''
  }
}

const generateForm = reactive<GenerateForm>(defaultGenerateForm())

const importForm = reactive({ name: '', cert_pem: '', key_pem: '', parent_id: 0 })

function keyTypeLabel(value: string): string {
  return keyTypes.find((item) => item.value === value)?.label || value
}

function caTypeLabel(ca: PrivateCA): string {
  return ca.ca_type === 'intermediate' ? t('privateCA.intermediateCA') : t('privateCA.rootCA')
}

function canIssueCA(ca: PrivateCA): boolean {
  return ca.path_len !== 0 && daysLeft(ca.not_after) > 0
}

async function load(): Promise<void> {
  loading.value = true
  try {
    items.value = (await api.get<PrivateCA[]>('/private-cas')) || []
    clearSelection()
    await loadMeta(true).catch(() => null)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}

function openGenerate(type: 'root' | 'intermediate' = 'root'): void {
  Object.assign(generateForm, defaultGenerateForm())
  generateForm.ca_type = type
  advancedOpen.value = []
  generateVisible.value = true
}

function openImport(): void {
  Object.assign(importForm, { name: '', cert_pem: '', key_pem: '', parent_id: 0 })
  importVisible.value = true
}

async function submitGenerate(): Promise<void> {
  if (!generateForm.name.trim()) {
    ElMessage.warning(t('privateCA.nameRequired'))
    return
  }
  if (generateForm.ca_type === 'intermediate' && !generateForm.parent_id) {
    ElMessage.warning(t('privateCA.parentRequired'))
    return
  }
  if (!generateForm.key_usage.includes('key_cert_sign')) {
    ElMessage.warning(t('privateCA.keyUsageRequired'))
    return
  }
  saving.value = true
  try {
    const data = await api.post<MessageResult>('/private-cas', {
      name: generateForm.name.trim(),
      common_name: generateForm.common_name.trim(),
      organization: generateForm.organization.trim(),
      organizational_unit: generateForm.organizational_unit.trim(),
      country: generateForm.country.trim(),
      state: generateForm.state.trim(),
      locality: generateForm.locality.trim(),
      email: generateForm.email.trim(),
      serial_number: generateForm.serial_number.trim(),
      key_type: generateForm.key_type,
      valid_days: generateForm.valid_days,
      parent_id: generateForm.ca_type === 'intermediate' ? generateForm.parent_id : 0,
      advanced: {
        path_len: generateForm.path_len,
        key_usage: generateForm.key_usage,
        ext_key_usage: generateForm.ext_key_usage,
        dns_names: splitLines(generateForm.dns_names),
        ip_addresses: splitLines(generateForm.ip_addresses),
        permitted_dns: splitLines(generateForm.permitted_dns),
        excluded_dns: splitLines(generateForm.excluded_dns),
        permitted_ips: splitLines(generateForm.permitted_ips),
        excluded_ips: splitLines(generateForm.excluded_ips),
        crl_urls: splitLines(generateForm.crl_urls),
        ocsp_urls: splitLines(generateForm.ocsp_urls),
        issuer_url: generateForm.issuer_url.trim(),
        policy_oids: splitLines(generateForm.policy_oids),
        cps_url: generateForm.cps_url.trim()
      }
    })
    ElMessage.success(data?.message || t('privateCA.generated'))
    generateVisible.value = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function submitImport(): Promise<void> {
  if (!importForm.name.trim()) {
    ElMessage.warning(t('privateCA.nameRequired'))
    return
  }
  if (!importForm.cert_pem.trim()) {
    ElMessage.warning(t('privateCA.certRequired'))
    return
  }
  if (!importForm.key_pem.trim()) {
    ElMessage.warning(t('privateCA.keyRequired'))
    return
  }
  saving.value = true
  try {
    const data = await api.post<MessageResult>('/private-cas/import', {
      name: importForm.name.trim(),
      cert_pem: importForm.cert_pem,
      key_pem: importForm.key_pem,
      parent_id: importForm.parent_id || 0
    })
    ElMessage.success(data?.message || t('privateCA.imported'))
    importVisible.value = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    saving.value = false
  }
}

async function downloadCA(ca: PrivateCA, kind: string): Promise<void> {
  try {
    const headers: Record<string, string> = {}
    if (kind === 'key') {
      const token = await requestSecondaryVerification('private_ca_key_export')
      if (!token) return
      headers[secondaryVerifyHeader] = token
    }
    const suffix = kind === 'key' ? 'key' : kind === 'chain' ? 'chain.crt' : 'crt'
    await downloadFile(`/private-cas/${ca.id}/download?type=${kind}`, `${ca.common_name || ca.name}-${suffix}`, {
      headers
    })
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function remove(ca: PrivateCA): Promise<void> {
  try {
    await ElMessageBox.confirm(t('privateCA.deleteConfirm', { name: ca.name }), t('privateCA.deleteTitle'), {
      confirmButtonText: t('privateCA.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  try {
    const data = await api.delete<MessageResult>(`/private-cas/${ca.id}`)
    ElMessage.success(data?.message || t('privateCA.deleted'))
    await load()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function onSelectionChange(rows: PrivateCA[]): void {
  selectedCAs.value = rows
}

function clearSelection(): void {
  tableRef.value?.clearSelection()
  selectedCAs.value = []
}

function caDepth(ca: PrivateCA): number {
  let depth = 0
  let parent = ca.parent_id
  const seen = new Set<number>()
  while (parent && !seen.has(parent)) {
    seen.add(parent)
    depth++
    parent = items.value.find((item) => item.id === parent)?.parent_id || 0
  }
  return depth
}

async function batchDelete(): Promise<void> {
  const list = [...selectedCAs.value]
  if (!list.length) return
  try {
    await ElMessageBox.confirm(t('privateCA.batchDeleteConfirm', { count: list.length }), t('privateCA.batchDeleteTitle'), {
      confirmButtonText: t('privateCA.delete'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch (_) {
    return
  }
  batchDeleting.value = true
  let failed = 0
  // 先删子 CA：父 CA 下仍有子级时后端会拒绝删除。
  const ordered = [...list].sort((a, b) => caDepth(b) - caDepth(a))
  try {
    for (const ca of ordered) {
      try {
        await api.delete(`/private-cas/${ca.id}`)
      } catch (error: any) {
        failed++
        ElMessage.error(`${ca.name}: ${error.message}`)
      }
    }
  } finally {
    batchDeleting.value = false
  }
  if (failed) ElMessage.warning(t('privateCA.batchDeleteFailed', { count: failed }))
  else ElMessage.success(t('privateCA.batchDeleted', { count: list.length }))
  await load()
}

onMounted(load)
</script>

<style scoped>
.ca-title {
  font-weight: 600;
  font-size: 14px;
  color: #0f172a;
}
.ca-desc {
  margin-top: 3px;
  color: #94a3b8;
  font-size: 12px;
  line-height: 1.5;
}
.form-help {
  font-size: 12px;
  color: #94a3b8;
  margin-top: 6px;
  line-height: 1.6;
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

:deep(.ca-actions-cell .cell) {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding-left: 0;
  padding-right: 0;
  white-space: nowrap;
}

:deep(.ca-actions-cell .cell .el-button) {
  margin-left: 0;
  padding: 0;
}
</style>
