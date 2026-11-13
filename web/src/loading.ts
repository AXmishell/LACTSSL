import { reactive } from 'vue'

export interface LoadingState {
  visible: boolean
  text: string
}

// 全局加载层状态：路由切换和显式异步任务共用一套状态，避免多个任务互相覆盖。
export const loadingState = reactive<LoadingState>({
  visible: false,
  text: ''
})

// 短于该时长的操作不闪出加载层，避免页面切换时出现一帧白影。
const SHOW_DELAY = 140
// 加载层已经出现后至少保留该时长，让动画完整可读。
const MIN_VISIBLE = 360

let routeTasks = 0
let manualTasks = 0
let showTimer: number | null = null
let hideTimer: number | null = null
let visibleSince = 0

function active(): boolean {
  return routeTasks > 0 || manualTasks > 0
}

function clearTimer(timer: number | null): null {
  if (timer !== null) window.clearTimeout(timer)
  return null
}

function sync(): void {
  if (active()) {
    hideTimer = clearTimer(hideTimer)
    if (loadingState.visible || showTimer !== null) return
    showTimer = window.setTimeout(() => {
      showTimer = null
      if (!active()) return
      visibleSince = performance.now()
      loadingState.visible = true
    }, SHOW_DELAY)
    return
  }

  showTimer = clearTimer(showTimer)
  if (!loadingState.visible) return
  const wait = Math.max(0, MIN_VISIBLE - (performance.now() - visibleSince))
  hideTimer = clearTimer(hideTimer)
  hideTimer = window.setTimeout(() => {
    hideTimer = null
    if (active()) return
    loadingState.visible = false
    loadingState.text = ''
  }, wait)
}

// 路由守卫在每次导航开始时调用，导航结束（含失败）时结束。
export function beginRouteLoading(): void {
  routeTasks += 1
  sync()
}

export function endRouteLoading(): void {
  routeTasks = Math.max(0, routeTasks - 1)
  sync()
}

// 业务代码需要在整页遮罩下等待时使用，例如批量导入、证书签发前的准备步骤。
// 与其他加载层共用延迟显示和最短展示时长，不提供特殊展示路径。
export function beginLoading(text = ''): void {
  manualTasks += 1
  if (text) loadingState.text = text
  sync()
}

export function endLoading(): void {
  manualTasks = Math.max(0, manualTasks - 1)
  sync()
}

export async function withLoading<T>(task: Promise<T> | (() => Promise<T>), text = ''): Promise<T> {
  beginLoading(text)
  try {
    return await (typeof task === 'function' ? task() : task)
  } finally {
    endLoading()
  }
}
