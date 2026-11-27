<template>
  <div class="page-size-select">
    <el-select
      v-model="selected"
      class="page-size-select__control"
      size="small"
      :disabled="disabled"
      @change="onSelect"
    >
      <el-option
        v-for="size in mergedOptions"
        :key="size"
        :value="size"
        :label="`${size} ${t('common.perPage')}`"
      />
      <el-option :value="CUSTOM" :label="t('common.custom')" />
    </el-select>

    <el-dialog
      v-model="customVisible"
      :title="t('common.pageSize')"
      width="320px"
      append-to-body
      class="page-size-dialog"
    >
      <el-input-number
        v-model="draft"
        :min="min"
        :max="max"
        :step="1"
        step-strictly
        controls-position="right"
        style="width: 100%"
        @keyup.enter="applyCustom"
      />
      <div class="page-size-select__hint muted">{{ min }} – {{ max }}</div>
      <template #footer>
        <el-button @click="customVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" @click="applyCustom">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { PAGE_SIZE_OPTIONS } from '../pageSize'

const CUSTOM = 'custom'
type SelectValue = number | typeof CUSTOM

const props = withDefaults(
  defineProps<{
    modelValue: number
    min?: number
    max?: number
    options?: number[]
    disabled?: boolean
  }>(),
  {
    min: 1,
    max: 200,
    options: () => PAGE_SIZE_OPTIONS,
    disabled: false
  }
)

const emit = defineEmits<{
  'update:modelValue': [value: number]
  change: [value: number]
}>()

const { t } = useI18n()
const selected = ref<SelectValue>(props.modelValue)
const customVisible = ref(false)
const draft = ref(props.modelValue)

watch(
  () => props.modelValue,
  (value) => {
    selected.value = value
  }
)

// 自定义值不在预设中时也给它一个可见选项，避免下拉框显示空白。
const mergedOptions = computed(() => {
  const list = [...props.options]
  if (!list.includes(props.modelValue)) list.push(props.modelValue)
  return list.sort((left, right) => left - right)
})

function clamp(value: number): number {
  if (!Number.isFinite(value)) return props.modelValue
  return Math.min(props.max, Math.max(props.min, Math.floor(value)))
}

function commit(value: number): void {
  const size = clamp(value)
  selected.value = size
  if (size === props.modelValue) return
  emit('update:modelValue', size)
  emit('change', size)
}

function onSelect(value: SelectValue): void {
  if (value === CUSTOM) {
    // 选择「自定义」先恢复原值，等弹窗确认后再提交，取消时不会留下错误显示。
    selected.value = props.modelValue
    draft.value = props.modelValue
    customVisible.value = true
    return
  }
  commit(Number(value))
}

function applyCustom(): void {
  customVisible.value = false
  commit(draft.value)
}
</script>

<style scoped>
.page-size-select {
  display: inline-flex;
  align-items: center;
}

.page-size-select__control {
  width: 132px;
}

.page-size-select__hint {
  margin-top: 8px;
  font-size: 12px;
  text-align: right;
}
</style>
