// 批量操作栏的延迟显示控制。
import { onBeforeUnmount, ref, watch } from 'vue'

// useDelayedBatchBar 在选中项出现后延迟 delay 毫秒再显示操作栏，
// 避免复选框勾选动画期间批量栏突然插入导致布局跳动；取消选择时立即隐藏。
// 各列表页统一使用 50ms，保持交互一致。
export function useDelayedBatchBar(hasSelection: () => boolean, delay = 50) {
  const visible = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined

  watch(hasSelection, (active) => {
    if (timer) {
      clearTimeout(timer)
      timer = undefined
    }
    if (!active) {
      visible.value = false
      return
    }
    if (visible.value) return
    timer = setTimeout(() => {
      visible.value = true
    }, delay)
  })

  onBeforeUnmount(() => {
    if (timer) clearTimeout(timer)
  })

  return visible
}
