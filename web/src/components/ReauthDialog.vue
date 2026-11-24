<template>
  <el-dialog
    v-model="reauthState.visible"
    :title="t('settings.reauth')"
    width="420px"
    :close-on-click-modal="false"
    :close-on-press-escape="false"
    :before-close="handleBeforeClose"
    append-to-body
    :z-index="2700"
  >
    <div class="reauth-dialog">
      <p class="reauth-hint">{{ t('settings.reauthHint') }}</p>
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item :label="t('settings.reauthPasswordLabel')">
          <el-input
            v-model="reauthState.password"
            type="password"
            show-password
            autocomplete="current-password"
            :placeholder="t('settings.reauthPasswordPlaceholder')"
            @keyup.enter="submit"
          />
        </el-form-item>
        <el-form-item :label="t('settings.reauthTotpLabel')">
          <el-input
            v-model="reauthState.totp"
            maxlength="6"
            inputmode="numeric"
            autocomplete="one-time-code"
            :placeholder="t('settings.reauthTotpPlaceholder')"
            @keyup.enter="submit"
          />
        </el-form-item>
      </el-form>
      <el-alert v-if="reauthState.error" :title="reauthState.error" type="error" :closable="false" show-icon />
    </div>
    <template #footer>
      <el-button @click="cancelReauth">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="reauthState.loading" @click="submit">
        {{ t('common.confirm') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { cancelReauth, reauthState, submitReauth } from '../api'

const { t } = useI18n()

function submit(): void {
  void submitReauth()
}

function handleBeforeClose(done: () => void): void {
  // 用户点击右上角关闭时按取消处理，避免等待中的请求一直挂起。
  cancelReauth()
  done()
}
</script>

<style scoped>
.reauth-hint {
  margin: 0 0 16px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
}
</style>
