import type { ElTooltipProps } from 'element-plus'
import { ref, shallowRef } from 'vue'

export interface OverflowTooltipTarget {
  el: HTMLElement
  content: string
  placement: ElTooltipProps['placement']
  popperClass: string
  label: string
  tone: 'success' | 'danger' | 'default'
}

// 悬停离开后保留一小段时间，方便指针移入提示框或移动到相邻单元格。
const HIDE_DELAY = 300

export const overflowTooltipTarget = shallowRef<OverflowTooltipTarget | null>(null)
export const overflowTooltipVisible = ref(false)
// 固定态由点击文字触发：提示框可交互、可选中文字，直到点击外部或按 Esc。
export const overflowTooltipPinned = ref(false)
let hideTimer: number | undefined

export function cancelOverflowTooltipHide(): void {
  if (hideTimer === undefined) return
  window.clearTimeout(hideTimer)
  hideTimer = undefined
}

// 延迟隐藏只在尚未安排时设置，避免指针持续移动不断推迟关闭。
export function scheduleOverflowTooltipHide(el?: HTMLElement): void {
  if (overflowTooltipPinned.value) return
  if (el && overflowTooltipTarget.value?.el !== el) return
  if (hideTimer !== undefined) return
  hideTimer = window.setTimeout(() => {
    hideTimer = undefined
    overflowTooltipVisible.value = false
  }, HIDE_DELAY)
}

export function showOverflowTooltip(target: OverflowTooltipTarget, pin = false): void {
  // 固定态下忽略其他单元格的悬停切换，避免阅读/选中时提示框跑走。
  if (!pin && overflowTooltipPinned.value && overflowTooltipTarget.value?.el !== target.el) return
  cancelOverflowTooltipHide()
  overflowTooltipTarget.value = target
  overflowTooltipVisible.value = true
  if (pin) overflowTooltipPinned.value = true
}

export function toggleOverflowTooltipPin(target: OverflowTooltipTarget): void {
  if (overflowTooltipPinned.value && overflowTooltipTarget.value?.el === target.el) {
    unpinOverflowTooltip()
    return
  }
  showOverflowTooltip(target, true)
}

export function unpinOverflowTooltip(): void {
  overflowTooltipPinned.value = false
  cancelOverflowTooltipHide()
  overflowTooltipVisible.value = false
}

export function isOverflowTooltipActive(el: HTMLElement): boolean {
  return overflowTooltipTarget.value?.el === el
}

export function resetOverflowTooltip(): void {
  cancelOverflowTooltipHide()
  overflowTooltipPinned.value = false
  overflowTooltipTarget.value = null
  overflowTooltipVisible.value = false
}
