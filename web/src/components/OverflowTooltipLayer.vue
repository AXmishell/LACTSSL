<script setup lang="ts">
import type { TooltipInstance } from 'element-plus'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { copyText } from '../api'
import {
  cancelOverflowTooltipHide,
  overflowTooltipPinned,
  overflowTooltipTarget,
  overflowTooltipVisible,
  resetOverflowTooltip,
  scheduleOverflowTooltipHide,
  unpinOverflowTooltip,
} from './overflowTooltip'

const { t } = useI18n()

const tooltipRef = ref<TooltipInstance | null>(null)
const bodyRef = ref<HTMLElement | null>(null)
const bodyClipped = ref(false)
const copyState = ref<'idle' | 'copied' | 'failed'>('idle')
let copyTimer: number | undefined

const virtualRef = computed(() => overflowTooltipTarget.value?.el)
const content = computed(() => overflowTooltipTarget.value?.content ?? '')
const label = computed(() => overflowTooltipTarget.value?.label ?? '')
const tone = computed(() => overflowTooltipTarget.value?.tone ?? 'default')
const placement = computed(() => overflowTooltipTarget.value?.placement ?? 'top')
const popperClass = computed(() =>
  [
    'overflow-tooltip-popper',
    overflowTooltipPinned.value ? 'is-pinned' : '',
    overflowTooltipTarget.value?.popperClass ?? '',
  ]
    .filter(Boolean)
    .join(' ')
)

const copyLabel = computed(() => {
  if (copyState.value === 'copied') return t('common.copied')
  if (copyState.value === 'failed') return t('common.copyFailed')
  return t('common.copy')
})

// 与视口边缘保留一点距离，避免提示框贴边或阴影被裁掉。
const popperOptions = {
  modifiers: [{ name: 'preventOverflow', options: { padding: 12 } }],
}

function popperElement(): HTMLElement | null {
  return document.querySelector<HTMLElement>('.overflow-tooltip-popper')
}

function insidePopper(x: number, y: number): boolean {
  const el = popperElement()
  if (!el) return false
  const rect = el.getBoundingClientRect()
  return x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom
}

// 提示框 pointer-events: none，不会挡住下方内容；这里根据指针位置决定保持还是关闭。
// 指针落在其他被省略的内容上时，对应的 mouseenter 会直接切换提示框。
function handlePointerMove(event: PointerEvent): void {
  if (!overflowTooltipVisible.value || overflowTooltipPinned.value) return
  const target = overflowTooltipTarget.value
  if (!target) return
  const node = event.target instanceof Node ? event.target : null
  if ((node && target.el.contains(node)) || insidePopper(event.clientX, event.clientY)) {
    cancelOverflowTooltipHide()
    return
  }
  scheduleOverflowTooltipHide()
}

// 固定态：点击提示框和锚点文字之外的位置时关闭。
function handlePointerDown(event: PointerEvent): void {
  if (!overflowTooltipPinned.value) return
  const node = event.target instanceof Node ? event.target : null
  if (!node) return
  const popper = popperElement()
  if (popper?.contains(node)) return
  if (overflowTooltipTarget.value?.el.contains(node)) return
  unpinOverflowTooltip()
}

function handleKeyDown(event: KeyboardEvent): void {
  if (event.key !== 'Escape') return
  if (overflowTooltipVisible.value) unpinOverflowTooltip()
}

function resetCopyState(): void {
  copyState.value = 'idle'
  if (copyTimer !== undefined) {
    window.clearTimeout(copyTimer)
    copyTimer = undefined
  }
}

async function handleCopy(): Promise<void> {
  if (!content.value) return
  const ok = await copyText(content.value)
  resetCopyState()
  copyState.value = ok ? 'copied' : 'failed'
  copyTimer = window.setTimeout(() => {
    copyTimer = undefined
    copyState.value = 'idle'
  }, 1600)
}

// 预览态内容过高时做底部渐隐提示，固定后可滚动查看全部。
function measureBody(): void {
  const el = bodyRef.value
  bodyClipped.value = !!el && el.scrollHeight - el.clientHeight > 2
}

// 切换到另一个单元格或进入/退出固定态时，提示框尺寸与锚点可能变化，需要重算位置。
watch(
  () => [overflowTooltipTarget.value?.el, overflowTooltipTarget.value?.content, overflowTooltipPinned.value],
  async () => {
    resetCopyState()
    if (!overflowTooltipVisible.value) {
      bodyClipped.value = false
      return
    }
    await nextTick()
    tooltipRef.value?.updatePopper()
    requestAnimationFrame(measureBody)
  }
)

onMounted(() => {
  window.addEventListener('pointermove', handlePointerMove, { passive: true })
  window.addEventListener('pointerdown', handlePointerDown)
  window.addEventListener('keydown', handleKeyDown)
})

onBeforeUnmount(() => {
  window.removeEventListener('pointermove', handlePointerMove)
  window.removeEventListener('pointerdown', handlePointerDown)
  window.removeEventListener('keydown', handleKeyDown)
  resetCopyState()
  resetOverflowTooltip()
})
</script>

<template>
  <el-tooltip
    ref="tooltipRef"
    :visible="overflowTooltipVisible"
    :virtual-ref="virtualRef"
    virtual-triggering
    :placement="placement"
    :popper-class="popperClass"
    :popper-options="popperOptions"
    :offset="8"
    effect="dark"
    :enterable="false"
  >
    <template #content>
      <div class="overflow-tooltip">
        <div v-if="label" class="overflow-tooltip-head">
          <span v-if="tone !== 'default'" class="overflow-tooltip-dot" :class="`is-${tone}`" />
          <span class="overflow-tooltip-title">{{ label }}</span>
          <button
            type="button"
            class="overflow-tooltip-copy is-head"
            :class="{ 'is-done': copyState === 'copied', 'is-failed': copyState === 'failed' }"
            :aria-label="copyLabel"
            @pointerdown.stop
            @click.stop="handleCopy"
          >
            {{ copyLabel }}
          </button>
        </div>
        <button
          v-else
          type="button"
          class="overflow-tooltip-copy is-floating"
          :class="{ 'is-done': copyState === 'copied', 'is-failed': copyState === 'failed' }"
          :aria-label="copyLabel"
          @pointerdown.stop
          @click.stop="handleCopy"
        >
          {{ copyLabel }}
        </button>
        <div
          ref="bodyRef"
          class="overflow-tooltip-body"
          :class="{ 'is-clipped': bodyClipped, 'has-head': !!label, 'has-floating': !label }"
        >
          <div class="overflow-tooltip-body-text">{{ content }}</div>
        </div>
      </div>
    </template>
  </el-tooltip>
</template>

<style>
.overflow-tooltip-popper.el-popper.is-dark {
  max-width: min(620px, calc(100vw - 32px));
  padding: 0;
  /* 固定 2px 边框宽度，预览态用透明色，避免固定时改变尺寸产生抖动。 */
  border: 2px solid transparent;
  background: rgba(17, 26, 44, 0.97);
  color: #e2e8f0;
  box-shadow: none;
  backdrop-filter: blur(16px) saturate(120%);
  -webkit-backdrop-filter: blur(16px) saturate(120%);
  pointer-events: none;
}

.overflow-tooltip-popper.el-popper.is-dark .el-popper__arrow::before {
  background: #131d31;
  border-width: 2px;
  border-color: transparent;
}

/* 固定态才接管指针事件，可拖动选中文字；预览态保持穿透，不遮挡其他行。 */
.overflow-tooltip-popper.el-popper.is-dark.is-pinned {
  pointer-events: auto;
  border-color: rgba(96, 165, 250, 0.65);
}

.overflow-tooltip-popper.el-popper.is-dark.is-pinned .el-popper__arrow::before {
  border-color: rgba(96, 165, 250, 0.65);
}

.overflow-tooltip-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px 8px 14px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.03);
}

.overflow-tooltip-title {
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: #cbd5e1;
}

.overflow-tooltip-dot {
  flex: none;
  width: 7px;
  height: 7px;
  background: #94a3b8;
  box-shadow: 0 0 0 3px rgba(148, 163, 184, 0.16);
}

.overflow-tooltip-dot.is-success {
  background: #34d399;
  box-shadow: 0 0 0 3px rgba(52, 211, 153, 0.16);
}

.overflow-tooltip-dot.is-danger {
  background: #f87171;
  box-shadow: 0 0 0 3px rgba(248, 113, 113, 0.16);
}

.overflow-tooltip {
  position: relative;
}

.overflow-tooltip-body {
  position: relative;
  padding: 10px 14px 12px;
  max-height: min(320px, 50vh);
  overflow: hidden;
}

.overflow-tooltip-body.has-floating .overflow-tooltip-body-text {
  padding-right: 60px;
}

/* 预览态内容超长时底部渐隐，固定后可滚动查看完整内容。 */
.overflow-tooltip-body.is-clipped {
  -webkit-mask-image: linear-gradient(to bottom, #000 calc(100% - 28px), transparent);
  mask-image: linear-gradient(to bottom, #000 calc(100% - 28px), transparent);
}

.overflow-tooltip-popper.el-popper.is-dark.is-pinned .overflow-tooltip-body {
  overflow: auto;
  scrollbar-width: thin;
  scrollbar-color: rgba(148, 163, 184, 0.4) transparent;
}

.overflow-tooltip-popper.el-popper.is-dark.is-pinned .overflow-tooltip-body.is-clipped {
  -webkit-mask-image: none;
  mask-image: none;
}

.overflow-tooltip-popper.el-popper.is-dark.is-pinned .overflow-tooltip-body::-webkit-scrollbar-thumb {
  background: rgba(148, 163, 184, 0.4);
}

.overflow-tooltip-body-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12.5px;
  line-height: 1.75;
  color: #dbe4f0;
  user-select: text;
  cursor: text;
}

.overflow-tooltip-body-text::selection {
  background: rgba(59, 130, 246, 0.45);
}

.overflow-tooltip-copy {
  pointer-events: auto;
  appearance: none;
  border: 1px solid rgba(148, 163, 184, 0.35);
  background: rgba(255, 255, 255, 0.06);
  color: #dbeafe;
  font-size: 11.5px;
  line-height: 20px;
  padding: 0 8px;
  cursor: pointer;
  transition: background 0.15s ease, border-color 0.15s ease, color 0.15s ease;
}

.overflow-tooltip-copy:hover {
  background: rgba(255, 255, 255, 0.14);
  border-color: rgba(191, 219, 254, 0.5);
}

.overflow-tooltip-copy.is-done {
  color: #86efac;
  border-color: rgba(134, 239, 172, 0.5);
}

.overflow-tooltip-copy.is-failed {
  color: #fca5a5;
  border-color: rgba(252, 165, 165, 0.5);
}

.overflow-tooltip-copy.is-head {
  margin-left: auto;
}

.overflow-tooltip-copy.is-floating {
  position: absolute;
  top: 8px;
  right: 10px;
}
</style>
