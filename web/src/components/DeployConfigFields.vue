<template>
  <template v-for="field in visibleFields" :key="field.key">
    <el-form-item :label="field.label" :required="field.required" :class="['deploy-field', `deploy-field-${field.type}`]">
      <template v-if="field.options_source && accountId">
        <el-select
          :model-value="selectValue(field)"
          :multiple="field.multiple"
          :loading="optionState(field).loading"
          filterable
          allow-create
          default-first-option
          clearable
          :collapse-tags="field.multiple"
          :collapse-tags-tooltip="field.multiple"
          style="width: 100%"
          :placeholder="field.placeholder"
          @update:model-value="(value: string | string[]) => updateSelect(field, value)"
        >
          <el-option v-for="option in optionsOf(field)" :key="option.value" :label="option.label" :value="option.value" />
        </el-select>
        <div class="form-help deploy-option-actions">
          <span>{{ t('deployFields.dynamicHint') }}</span>
          <el-button link type="primary" size="small" :loading="optionState(field).loading" @click="loadOptions(field, true)">
            {{ t('deployFields.refresh') }}
          </el-button>
        </div>
        <div v-if="optionState(field).error" class="form-help warn">{{ optionState(field).error }}</div>
      </template>
      <el-select
        v-else-if="field.type === 'select'"
        :model-value="valueOf(field)"
        style="width: 100%"
        @update:model-value="(value: string) => update(field.key, value)"
      >
        <el-option v-for="option in field.options || []" :key="option.value" :label="option.label" :value="option.value" />
      </el-select>
      <el-switch
        v-else-if="field.type === 'switch'"
        :model-value="valueOf(field) === 'true'"
        @update:model-value="(value: boolean) => update(field.key, value ? 'true' : 'false')"
      />
      <el-input
        v-else-if="field.type === 'textarea'"
        :model-value="valueOf(field)"
        type="textarea"
        :rows="5"
        :placeholder="field.placeholder"
        class="mono"
        @update:model-value="(value: string) => update(field.key, value)"
      />
      <el-input
        v-else-if="field.type === 'password'"
        :model-value="valueOf(field)"
        type="password"
        show-password
        :placeholder="field.placeholder"
        @update:model-value="(value: string) => update(field.key, value)"
      />
      <el-input
        v-else
        :model-value="valueOf(field)"
        :placeholder="field.placeholder"
        @update:model-value="(value: string) => update(field.key, value)"
      />
      <div v-if="field.help" class="form-help">{{ field.help }}</div>
    </el-form-item>
  </template>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, errorText } from '../api'
import { deployDomainMatchesSite, deployObjectFieldKey } from '../deployConfig'
import { state } from '../store'
import type { FieldDef, FieldOption } from '../types'
import { fieldVisible } from '../utils'

interface OptionsResponse {
  options?: FieldOption[]
}

interface MatchResponse {
  field?: string
  multiple?: boolean
  values?: string[]
}

interface OptionState {
  key: string
  loading: boolean
  error: string
}

const AUTO_MATCH_DELAY = 400

const props = defineProps<{
  accountType: string
  accountId?: number
  modelValue: Record<string, string>
  // 目标流程 Beta 的站点字段由读取结果自动填充，基础配置中隐藏。
  excludeKeys?: string[]
  // 证书申请域名。传入后，任务部署节点会自动选中与域名匹配的网站名称。
  matchDomains?: string[]
}>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: Record<string, string>): void
}>()

const { t } = useI18n()

const fields = computed<FieldDef[]>(() => {
  const def = (state.meta?.deploy_types || []).find((item) => item.value === props.accountType)
  return def?.deploy_fields || []
})

const visibleFields = computed(() => {
  const excluded = new Set(props.excludeKeys || [])
  return fields.value.filter(
    (field) => !excluded.has(field.key) && fieldVisible(field, fields.value, props.modelValue)
  )
})

const optionStates = reactive<Record<string, OptionState>>({})
const dynamicOptions = reactive<Record<string, FieldOption[]>>({})

// 切换凭据或部署对象时重新读取远端选项；同一目标不重复请求，刷新按钮可强制重读。
watch(
  () => [props.accountId, props.accountType, props.modelValue?.target],
  () => {
    for (const field of visibleFields.value) {
      if (field.options_source) {
        void loadOptions(field, false)
      }
    }
  },
  { immediate: true }
)

const emptyOptionState: OptionState = { key: '', loading: false, error: '' }

// 任务部署节点只对“网站/站点”字段自动匹配，证书路径、重载命令等字段不受影响。
const matchSiteField = computed<FieldDef | undefined>(() => {
  if (!props.matchDomains?.length) return undefined
  const key = deployObjectFieldKey(props.accountType, props.modelValue)
  if (!key) return undefined
  return fields.value.find((field) => field.key === key)
})

// 记录自动填入的值，用户手动改选后不再覆盖；清空后允许按新域名重新匹配。
const autoSelected = reactive<Record<string, string>>({})
let autoMatchTimer: ReturnType<typeof setTimeout> | undefined
let autoMatchSeq = 0

watch(
  () => [
    props.accountId,
    props.accountType,
    matchSiteField.value?.key,
    (props.matchDomains || []).join(','),
    props.modelValue?.target
  ],
  () => scheduleAutoMatch(),
  { immediate: true }
)

onBeforeUnmount(() => {
  if (autoMatchTimer) clearTimeout(autoMatchTimer)
  autoMatchSeq += 1
})

function scheduleAutoMatch(): void {
  if (autoMatchTimer) clearTimeout(autoMatchTimer)
  if (!props.accountId || !matchSiteField.value || !(props.matchDomains || []).length) return
  // 域名输入通常逐字变化，稍作等待避免每敲一个字符就请求一次面板。
  autoMatchTimer = setTimeout(() => {
    autoMatchTimer = undefined
    void autoMatchSiteField()
  }, AUTO_MATCH_DELAY)
}

async function autoMatchSiteField(): Promise<void> {
  const field = matchSiteField.value
  const accountId = props.accountId
  const domains = normalizeDomains(props.matchDomains || [])
  if (!field || !accountId || !domains.length) return
  const stateKey = `${accountId}|${field.key}`
  const current = valueOf(field).trim()
  const previous = autoSelected[stateKey] || ''
  // 已有非自动填入的值视为用户手动选择，不能覆盖。
  if (current && current !== previous) return

  const seq = ++autoMatchSeq
  let values: string[] = []
  let multiple = !!field.multiple
  try {
    const result = await api.post<MatchResponse>(`/deploy-accounts/${accountId}/match`, {
      domains,
      config: { ...props.modelValue }
    })
    if (seq !== autoMatchSeq) return
    // plan 返回的多值能力比 manifest 静态字段更准确。
    if (result?.multiple) multiple = true
    values = normalizeMatchedValues(result?.values)
  } catch (_) {
    if (seq !== autoMatchSeq) return
    // 面板不可达或旧插件不支持 plan 时，退回按已读取的选项名称匹配。
    values = matchLoadedOptions(field, domains)
  }
  if (seq !== autoMatchSeq) return
  // 单值字段命中多个网站时无法判断该部署到哪一个，留给用户手动选择。
  if (!multiple && values.length > 1) values = []

  const next = values.join(',')
  if (next === current) {
    if (next) autoSelected[stateKey] = next
    else delete autoSelected[stateKey]
    return
  }
  if (!next && current !== previous) return
  update(field.key, next)
  if (next) autoSelected[stateKey] = next
  else delete autoSelected[stateKey]
}

function matchLoadedOptions(field: FieldDef, domains: string[]): string[] {
  const out: string[] = []
  for (const option of optionsOf(field)) {
    if (!deployDomainMatchesSite(option.value, domains) && !deployDomainMatchesSite(option.label, domains)) continue
    if (option.value && !out.includes(option.value)) out.push(option.value)
  }
  return out
}

function normalizeMatchedValues(values?: string[]): string[] {
  const out: string[] = []
  for (const value of values || []) {
    const item = String(value || '').trim()
    if (item && !out.includes(item)) out.push(item)
  }
  return out
}

function normalizeDomains(values: string[]): string[] {
  const out: string[] = []
  for (const value of values) {
    const item = String(value || '').trim().toLowerCase().replace(/\.$/, '')
    if (item && !out.includes(item)) out.push(item)
  }
  return out
}

function optionState(field: FieldDef): OptionState {
  return optionStates[field.key] || emptyOptionState
}

function optionsOf(field: FieldDef): FieldOption[] {
  return dynamicOptions[field.key] ?? field.options ?? []
}

function optionKey(field: FieldDef): string {
  return `${props.accountId || 0}|${field.key}|${props.modelValue?.target || ''}|${field.options_source || ''}`
}

async function loadOptions(field: FieldDef, force: boolean): Promise<void> {
  if (!optionStates[field.key]) {
    optionStates[field.key] = { key: '', loading: false, error: '' }
  }
  const option = optionStates[field.key]
  if (!props.accountId) {
    option.error = ''
    return
  }
  const key = optionKey(field)
  if (!force && option.key === key) return
  if (option.key !== key) dynamicOptions[field.key] = []
  option.key = key
  option.loading = true
  option.error = ''
  try {
    const result = await api.post<OptionsResponse>(`/deploy-accounts/${props.accountId}/options`, {
      field: field.key,
      config: { ...props.modelValue }
    })
    if (option.key !== key) return
    dynamicOptions[field.key] = result?.options || []
  } catch (error) {
    if (option.key !== key) return
    dynamicOptions[field.key] = []
    option.error = errorText(error)
  } finally {
    if (option.key === key) option.loading = false
  }
}

function valueOf(field: FieldDef): string {
  const value = props.modelValue?.[field.key]
  if (value !== undefined) return value
  return field.default || ''
}

function selectValue(field: FieldDef): string | string[] {
  const value = valueOf(field)
  if (!field.multiple) return value
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function updateSelect(field: FieldDef, value: string | string[]): void {
  update(field.key, Array.isArray(value) ? value.join(',') : value)
}

function update(key: string, value: string): void {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}
</script>

<style scoped>
.deploy-option-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
</style>
