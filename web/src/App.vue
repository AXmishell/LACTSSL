<template>
  <el-config-provider :locale="elementLocale">
    <router-view />
  </el-config-provider>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { elementLocaleFor } from './i18n'

// 语言切换时同步 Element Plus 内置文案（分页、空状态、日期选择等）。
const { locale } = useI18n()
const elementLocale = computed(() => elementLocaleFor(locale.value))
</script>
