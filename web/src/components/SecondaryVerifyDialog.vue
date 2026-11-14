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
      <el-form-item v-if="verifyModeNeedsPasskey(state.mode)" :label="t('settings.passkeyVerify')" required>
        <div class="flex gap-10 wrap" style="align-items: center">
          <el-button :loading="state.loading" :disabled="!passkeyUsable" @click="verifyWithPasskey">
            {{ state.passkeyProof ? t('settings.passkeyVerified') : t('settings.passkeyVerifyAction') }}
          </el-button>
          <span v-if="state.passkeyProof" class="tag-soft tag-green">{{ t('settings.passkeyVerified') }}</span>
        </div>
        <div v-if="!passkeyUsable" class="form-help">{{ t('settings.passkeyUnavailable') }}</div>
        <div v-else class="form-help">{{ t('settings.passkeyVerifyHint') }}</div>
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
  verifyModeNeedsPasskey,
  verifyModeNeedsTOTP,
  verifySecondary
} from '../secondaryVerify'
import { isPasskeyCancelled, passkeySecureContext, passkeySupported, requestPasskeyProof } from '../passkeys'

const { t } = useI18n()
const passkeyUsable = passkeySupported() && passkeySecureContext()

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
  if (verifyModeNeedsPasskey(state.mode) && !state.passkeyProof) {
    ElMessage.warning(t('settings.passkeyRequired'))
    return
  }
  state.loading = true
  state.error = ''
  try {
    const token = await verifySecondary(state.action, state.password, state.totp, state.passkeyProof)
    finishSecondaryVerify(token)
  } catch (error: any) {
    if (error?.totpRequired || error?.passkeyRequired) {
      // 本地验证方式设置过期时，刷新后显示动态验证码或通行密钥按钮。
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

async function verifyWithPasskey(): Promise<void> {
  if (state.loading) return
  state.loading = true
  state.error = ''
  try {
    state.passkeyProof = await requestPasskeyProof()
    // 纯通行密钥模式拿到证明后直接提交，减少一次确认点击。
    if (!verifyModeNeedsPassword(state.mode)) {
      state.loading = false
      await submit()
    }
  } catch (error: any) {
    state.error = isPasskeyCancelled(error) ? t('settings.passkeyCancelled') : error?.message || t('common.operationFailed')
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
