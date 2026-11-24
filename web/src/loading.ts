import { reactive } from 'vue'

// 两套加载反馈，职责分开：
// - 路由切换只驱动顶部细进度条，不遮挡页面内容，也不阻塞后续交互。
// - 显式阻塞任务（登录、批量导入等）才唤出全屏加载层。
// 两者互不影响，避免路由切换时闪出整页遮罩。

export interface RouteProgressState {
  visible: boolean
  value: number
}

// 顶部进度条：value 为 0-100，进行中缓慢逼近上限，结束时补满再淡出。
export const routeProgress = reactive<RouteProgressState>({
  visible: false,
  value: 0
})

export interface LoadingState {
  visible: boolean
  text: string
}

// 全屏加载层状态：只由 beginLoading / endLoading 控制。
export const loadingState = reactive<LoadingState>({
  visible: false,
  text: ''
})

// 进度条短于该时长的导航不出现，避免点一下就闪一条线。
const PROGRESS_DELAY = 120
// 到达 100% 后的停留时长，让用户看清"已完成"。
const PROGRESS_DONE_HOLD = 220
// 与 CSS 中的淡出时长一致，淡出结束后再重置宽度。
const PROGRESS_FADE = 220
// 进行中最多推进到该值，剩下的留给导航结束时补齐。
const PROGRESS_CEILING = 92
const PROGRESS_TICK = 200

// 短于该时长的阻塞任务不闪出加载层。
const SHOW_DELAY = 140
// 加载层已经出现后至少保留该时长，让动画完整可读。
const MIN_VISIBLE = 360

let routeTasks = 0
let progressShowTimer: number | null = null
let progressTickTimer: number | null = null
let progressHideTimer: number | null = null
let progressResetTimer: number | null = null

let manualTasks = 0
let overlayShowTimer: number | null = null
let overlayHideTimer: number | null = null
let overlayVisibleSince = 0

function clearTimer(timer: number | null): null {
  if (timer !== null) window.clearTimeout(timer)
  return null
}

function stopProgressTick(): void {
  if (progressTickTimer !== null) {
    window.clearInterval(progressTickTimer)
    progressTickTimer = null
  }
}

function startProgressTick(): void {
  stopProgressTick()
  progressTickTimer = window.setInterval(() => {
    const remaining = PROGRESS_CEILING - routeProgress.value
    if (remaining <= 0.5) return
    // 越接近上限推进越慢，避免长导航时进度条卡在中段显得停滞。
    routeProgress.value = Math.min(PROGRESS_CEILING, routeProgress.value + Math.max(0.5, remaining * 0.12))
  }, PROGRESS_TICK)
}

// beginRouteLoading / endRouteLoading 由路由守卫成对调用。
export function beginRouteLoading(): void {
  routeTasks += 1
  progressHideTimer = clearTimer(progressHideTimer)
  progressResetTimer = clearTimer(progressResetTimer)
  // 上一次进度条已淡出但尚未归零时，先回到起点，避免新导航从右向左缩回。
  if (!routeProgress.visible) routeProgress.value = 0

  if (routeProgress.visible) {
    // 上一次导航刚结束、进度条还在停留或淡出时，直接复用同一条进度条。
    if (routeProgress.value >= 100) routeProgress.value = 12
    if (progressTickTimer === null) startProgressTick()
    return
  }

  if (progressShowTimer !== null) return
  progressShowTimer = window.setTimeout(() => {
    progressShowTimer = null
    if (routeTasks === 0) return
    routeProgress.value = 12
    routeProgress.visible = true
    startProgressTick()
  }, PROGRESS_DELAY)
}

export function endRouteLoading(): void {
  // 首屏导航与公开页之间的切换不会调用 begin，忽略多余的结束。
  if (routeTasks === 0) return
  routeTasks -= 1
  if (routeTasks > 0) return

  progressShowTimer = clearTimer(progressShowTimer)
  if (!routeProgress.visible) {
    routeProgress.value = 0
    return
  }

  stopProgressTick()
  routeProgress.value = 100
  progressHideTimer = window.setTimeout(() => {
    progressHideTimer = null
    if (routeTasks > 0) return
    routeProgress.visible = false
    // 等淡出完成再归零，否则会看到进度条从右向左缩回。
    progressResetTimer = window.setTimeout(() => {
      progressResetTimer = null
      if (!routeProgress.visible) routeProgress.value = 0
    }, PROGRESS_FADE)
  }, PROGRESS_DONE_HOLD)
}

function overlayActive(): boolean {
  return manualTasks > 0
}

function syncOverlay(): void {
  if (overlayActive()) {
    overlayHideTimer = clearTimer(overlayHideTimer)
    if (loadingState.visible || overlayShowTimer !== null) return
    overlayShowTimer = window.setTimeout(() => {
      overlayShowTimer = null
      if (!overlayActive()) return
      overlayVisibleSince = performance.now()
      loadingState.visible = true
    }, SHOW_DELAY)
    return
  }

  overlayShowTimer = clearTimer(overlayShowTimer)
  if (!loadingState.visible) {
    // 任务在延迟期内就结束了：遮罩从未出现，必须立刻清空文案，
    // 否则下一次加载会带上这次残留的提示语。
    loadingState.text = ''
    return
  }

  const wait = Math.max(0, MIN_VISIBLE - (performance.now() - overlayVisibleSince))
  overlayHideTimer = clearTimer(overlayHideTimer)
  overlayHideTimer = window.setTimeout(() => {
    overlayHideTimer = null
    if (overlayActive()) return
    loadingState.visible = false
    loadingState.text = ''
  }, wait)
}

// 业务代码需要在整页遮罩下等待时使用，例如登录跳转、批量导入、证书签发前的准备步骤。
export function beginLoading(text = ''): void {
  manualTasks += 1
  if (text) loadingState.text = text
  syncOverlay()
}

export function endLoading(): void {
  manualTasks = Math.max(0, manualTasks - 1)
  syncOverlay()
}

export async function withLoading<T>(task: Promise<T> | (() => Promise<T>), text = ''): Promise<T> {
  beginLoading(text)
  try {
    return await (typeof task === 'function' ? task() : task)
  } finally {
    endLoading()
  }
}
