<template>
  <el-form-item v-if="totpEnabled" :label="t('settings.reauthTotpLabel')">
    <el-input
      v-model="totpValue"
      maxlength="6"
      inputmode="numeric"
      autocomplete="one-time-code"
      :placeholder="t('settings.reauthTotpPlaceholder')"
    />
    <div class="form-help">{{ t('settings.reauthHint') }}</div>
  </el-form-item>
  <el-form-item v-else :label="t('settings.reauthPasswordLabel')">
    <el-input
      v-model="passwordValue"
      type="password"
      show-password
      autocomplete="current-password"
      :placeholder="t('settings.reauthPasswordPlaceholder')"
    />
    <div class="form-help">{{ t('settings.reauthHint') }}</div>
  </el-form-item>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  password: string
  totp: string
  totpEnabled: boolean
}>()

const emit = defineEmits<{
  'update:password': [value: string]
  'update:totp': [value: string]
}>()

const passwordValue = computed({
  get: () => props.password,
  set: (value: string) => emit('update:password', value)
})
const totpValue = computed({
  get: () => props.totp,
  set: (value: string) => emit('update:totp', value)
})

const { t } = useI18n()
</script>
