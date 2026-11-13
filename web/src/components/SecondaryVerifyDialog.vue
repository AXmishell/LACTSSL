<template>
  <el-dialog
    v-model="state.visible"
    :title="t('secondaryVerify.title')"
    width="460px"
    append-to-body
    :close-on-click-modal="false"
    @closed="handleClosed"
  >
    <p class="muted" style="margin-top: 0">
      {{ t('secondaryVerify.desc', { action: t(secondaryActionLabelKeys[state.action]) }) }}
    </p>
    <el-alert
      v-if="state.mode === 'none'"
      type="warning"
      :closable="false"
      show-icon
      :title="t('secondaryVerify.noneHint')"
      style="margin-bottom: 16px"
    />
    <el-form label-position="top" @submit.prevent="submit">
      <el-form-item v-if="verifyModeNeedsPassword(state.mode)" :label="t('settings.currentPassword')" required>
        <el-input
          v-model="state.password"
          type="password"
          show-password
          autocomplete="current-password"
          :placeholder="t('settings.confirmIdentity')"
          @keyup.enter="submit"
        />
      </el-form-item>
      <el-form-item v-if="verifyModeNeedsTOTP(state.mode)" :label="t('settings.totpCode')" required>
        <el-input
          v-model="state.totp"
          maxlength="6"
          :placeholder="t('settings.totpCodePlaceholder')"
          @keyup.enter="submit"
        />
        <div class="form-help">{{ t('settings.criticalVerifyTotpHint') }}</div>
      </el-form-item>
    </el-form>
    <el-alert
      v-if="state.error"
      type="error"
      :closable="false"
      show-icon
      :title="state.error"
    />
    <template #footer>
      <el-button @click="cancel">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="state.loading" @click="submit">
        {{ t('common.confirm') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import {
  currentVerifyMode,
  finishSecondaryVerify,
  secondaryActionLabelKeys,
  secondaryVerifyState as state,
  verifyModeNeedsPassword,
  verifyModeNeedsTOTP,
  verifySecondary
} from '../secondaryVerify'

const { t } = useI18n()

async function submit(): Promise<void> {
  if (state.loading) return
  if (verifyModeNeedsPassword(state.mode) && !state.password) {
    ElMessage.warning(t('settings.currentPasswordRequired'))
    return
  }
  if (verifyModeNeedsTOTP(state.mode) && !/^\d{6}$/.test(state.totp)) {
    ElMessage.warning(t('settings.totpCodeInvalid'))
    return
  }
  state.loading = true
  state.error = ''
  try {
    const token = await verifySecondary(state.action, state.password, state.totp)
    finishSecondaryVerify(token)
  } catch (error: any) {
    if (error?.totpRequired) {
      // 本地验证方式设置过期时，刷新后显示动态验证码输入框。
      try {
        state.mode = await currentVerifyMode()
      } catch (_) {
        // 保留原模式，用户仍可取消后刷新页面。
      }
    }
    state.error = error?.message || t('common.operationFailed')
  } finally {
    state.loading = false
  }
}

function cancel(): void {
  finishSecondaryVerify(null)
}

function handleClosed(): void {
  if (state.resolve) {
    finishSecondaryVerify(null)
  }
}
</script>
