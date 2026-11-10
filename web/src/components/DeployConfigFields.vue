<template>
  <template v-for="field in visibleFields" :key="field.key">
    <el-form-item :label="field.label" :required="field.required">
      <el-select
        v-if="field.type === 'select'"
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
import { computed } from 'vue'
import { state } from '../store'
import type { FieldDef } from '../types'

const props = defineProps<{
  accountType: string
  modelValue: Record<string, string>
}>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: Record<string, string>): void
}>()

const fields = computed<FieldDef[]>(() => {
  const def = (state.meta?.deploy_types || []).find((item) => item.value === props.accountType)
  return def?.deploy_fields || []
})

const visibleFields = computed(() => {
  return fields.value.filter((field) => {
    if (!field.show_if) return true
    const source = fields.value.find((item) => item.key === field.show_if?.key)
    const value = props.modelValue?.[field.show_if.key] || source?.default || ''
    return value === field.show_if.value
  })
})

function valueOf(field: FieldDef): string {
  const value = props.modelValue?.[field.key]
  if (value !== undefined) return value
  return field.default || ''
}

function update(key: string, value: string): void {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}
</script>
