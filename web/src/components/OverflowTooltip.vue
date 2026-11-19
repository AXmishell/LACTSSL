<script setup lang="ts">
import type { ElTooltipProps } from 'element-plus'
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  isOverflowTooltipActive,
  overflowTooltipPinned,
  scheduleOverflowTooltipHide,
  showOverflowTooltip,
  toggleOverflowTooltipPin,
  unpinOverflowTooltip,
} from './overflowTooltip'

const props = withDefaults(
  defineProps<{
    content: string
    placement?: ElTooltipProps['placement']
    popperClass?: string
    label?: string
    tone?: 'success' | 'danger' | 'default'
    /** 内容未超出时也显示提示框（例如部署结果需要固定展示/复制）。 */
    always?: boolean
  }>(),
  {
    placement: 'top',
    popperClass: '',
    label: '',
    tone: 'default',
    always: false,
  }
)

const textRef = ref<HTMLSpanElement | null>(null)
const overflowing = ref(false)
let observer: ResizeObserver | null = null
let frame = 0

function cellOf(text: HTMLElement | null): HTMLElement | null {
  if (!text) return null
  return (text.closest('.cell') as HTMLElement | null) ?? text.parentElement
}

// 以单元格实际可用宽度对比文本自然宽度，只有真正被省略时才允许弹出提示。
function measure(): void {
  const text = textRef.value
  const cell = cellOf(text)
  if (!text || !cell) return
  const style = window.getComputedStyle(cell)
  const horizontalPadding = (Number.parseFloat(style.paddingLeft) || 0) + (Number.parseFloat(style.paddingRight) || 0)
  const available = cell.clientWidth - horizontalPadding
  if (available <= 0) {
    overflowing.value = false
    return
  }
  const range = document.createRange()
  range.selectNodeContents(text)
  overflowing.value = range.getBoundingClientRect().width - available > 1
}

function scheduleMeasure(): void {
  if (frame) cancelAnimationFrame(frame)
  frame = requestAnimationFrame(() => {
    frame = 0
    measure()
  })
}

function targetOf(el: HTMLElement) {
  return {
    el,
    content: props.content,
    placement: props.placement,
    popperClass: props.popperClass,
    label: props.label,
    tone: props.tone,
  }
}

function showCurrent(): void {
  const el = textRef.value
  if (!el) return
  showOverflowTooltip(targetOf(el))
}

function hasTooltip(): boolean {
  return props.always || overflowing.value
}

function handleMouseEnter(): void {
  // 进入时即测量一次，避免内容或列宽刚变化时用到旧状态。
  measure()
  if (hasTooltip()) showCurrent()
}

function handleMouseLeave(): void {
  const el = textRef.value
  if (el) scheduleOverflowTooltipHide(el)
}

// 点击文字会固定提示框，进入可选中、可复制状态。
function handleClick(): void {
  const el = textRef.value
  if (!el) return
  measure()
  if (!hasTooltip()) return
  toggleOverflowTooltipPin(targetOf(el))
}

onMounted(async () => {
  await nextTick()
  measure()
  if (typeof ResizeObserver === 'undefined') return
  observer = new ResizeObserver(scheduleMeasure)
  if (textRef.value) observer.observe(textRef.value)
  const cell = cellOf(textRef.value)
  if (cell) observer.observe(cell)
})

watch(
  () => props.content,
  async () => {
    await nextTick()
    measure()
    const el = textRef.value
    if (!el || !isOverflowTooltipActive(el)) return
    if (hasTooltip()) showCurrent()
    else if (overflowTooltipPinned.value) unpinOverflowTooltip()
    else scheduleOverflowTooltipHide(el)
  }
)

onBeforeUnmount(() => {
  observer?.disconnect()
  observer = null
  if (frame) cancelAnimationFrame(frame)
  const el = textRef.value
  if (!el || !isOverflowTooltipActive(el)) return
  if (overflowTooltipPinned.value) unpinOverflowTooltip()
  else scheduleOverflowTooltipHide(el)
})
</script>

<template>
  <span
    ref="textRef"
    class="overflow-tooltip-text"
    :class="{ 'is-overflowing': always || overflowing }"
    @mouseenter="handleMouseEnter"
    @mouseleave="handleMouseLeave"
    @click="handleClick"
  ><slot>{{ content }}</slot></span>
</template>

<style scoped>
.overflow-tooltip-text {
  display: inline;
}

.overflow-tooltip-text.is-overflowing {
  cursor: pointer;
}
</style>
