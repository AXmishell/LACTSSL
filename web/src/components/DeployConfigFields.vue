<template>
  <template v-for="field in visibleFields" :key="field.key">
    <el-form-item :label="field.label" :required="field.required">
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
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, errorText } from '../api'
import { state } from '../store'
import type { FieldDef, FieldOption } from '../types'
import { fieldVisible } from '../utils'

interface OptionsResponse {
  options?: FieldOption[]
}

interface OptionState {
  key: string
  loading: boolean
  error: string
}

const props = defineProps<{
  accountType: string
  accountId?: number
  modelValue: Record<string, string>
  // 目标流程 Beta 的站点字段由读取结果自动填充，基础配置中隐藏。
  excludeKeys?: string[]
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
