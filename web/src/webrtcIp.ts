// WebRTC 出口 IP 探测：作为 API 密钥 IP 白名单的浏览器侧诊断辅助。
//
// 使用前必须了解：
// - 只能在浏览器里运行，API 客户端（curl、CI、服务端脚本）无法通过它上报 IP；
// - 探测需要连接公共 STUN 服务器，会把本机出口 IP 暴露给对方，因此只在用户
//   显式点击时调用，不做页面加载或后台轮询；
// - 现代浏览器会把 host candidate 混淆成 mDNS（xxx.local），内网地址通常拿不到，
//   真正有参考价值的是 STUN 反射出的公网出口 IP；
// - 结果可能与 API 请求的真实来源 IP 不同，例如浏览器走代理而服务器走另一条出口。

export type WebRTCIPScope = 'public' | 'local'

export interface WebRTCIPAddress {
  ip: string
  scope: WebRTCIPScope
  /** ICE candidate 类型：srflx、host、prflx、relay 等。 */
  type: string
  protocol: string
}

export interface WebRTCIPOptions {
  /** STUN 服务器列表，默认使用内置公共 STUN。 */
  stunServers?: string[]
  /** 最长等待时间，默认 3 秒。 */
  timeoutMs?: number
}

// 同时覆盖中国大陆与海外网络环境；如需完全可控，可替换为自建 STUN。
export const WEBRTC_STUN_SERVERS = ['stun:stun.cloudflare.com:3478', 'stun:stun.miwifi.com:3478']

const IPV4_RE = /^(?:\d{1,3}\.){3}\d{1,3}$/
const IPV6_RE = /^[0-9a-f:]+$/i
const CANDIDATE_ADDRESS_RE = /^(?:a=)?candidate:\S+\s+\d+\s+\S+\s+\d+\s+(\S+)/

export function isWebRTCSupported(): boolean {
  return typeof window !== 'undefined' && typeof window.RTCPeerConnection === 'function'
}

export async function detectWebRTCIPs(options: WebRTCIPOptions = {}): Promise<WebRTCIPAddress[]> {
  if (!isWebRTCSupported()) throw new Error('webrtc-unsupported')

  const stunServers = options.stunServers?.length ? options.stunServers : WEBRTC_STUN_SERVERS
  const timeoutMs = options.timeoutMs ?? 3000
  const pc = new RTCPeerConnection({ iceServers: [{ urls: stunServers }] })
  const found = new Map<string, WebRTCIPAddress>()

  const remember = (candidate: RTCIceCandidate) => {
    const ip = candidateIP(candidate)
    if (!ip) return
    const entry: WebRTCIPAddress = {
      ip,
      scope: classifyIP(ip, candidate.type || ''),
      type: candidate.type || 'unknown',
      protocol: candidate.protocol || ''
    }
    const existing = found.get(ip)
    // 同一个 IP 同时以 host 与 srflx 出现时，优先保留“公网”语义。
    if (!existing || (existing.scope === 'local' && entry.scope === 'public')) {
      found.set(ip, entry)
    }
  }

  try {
    // 没有媒体流时创建 data channel 可以确保浏览器真正开始收集候选地址。
    pc.createDataChannel('lactssl-ip-probe')
    pc.onicecandidate = (event) => {
      if (event.candidate) remember(event.candidate)
    }
    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    await waitForGathering(pc, timeoutMs)
  } finally {
    pc.close()
  }

  return sortAddresses([...found.values()])
}

function waitForGathering(pc: RTCPeerConnection, timeoutMs: number): Promise<void> {
  if (pc.iceGatheringState === 'complete') return Promise.resolve()
  return new Promise((resolve) => {
    let timer = 0
    const finish = () => {
      window.clearTimeout(timer)
      pc.removeEventListener('icegatheringstatechange', onStateChange)
      pc.removeEventListener('icecandidate', onCandidate)
      resolve()
    }
    const onStateChange = () => {
      if (pc.iceGatheringState === 'complete') finish()
    }
    const onCandidate = (event: RTCPeerConnectionIceEvent) => {
      // candidate 为 null 表示收集结束。
      if (!event.candidate) finish()
    }
    timer = window.setTimeout(finish, timeoutMs)
    pc.addEventListener('icegatheringstatechange', onStateChange)
    pc.addEventListener('icecandidate', onCandidate)
  })
}

function candidateIP(candidate: RTCIceCandidate): string | null {
  let raw = candidate.address || ''
  if (!raw) {
    const match = CANDIDATE_ADDRESS_RE.exec(candidate.candidate.trim())
    raw = match ? match[1] : ''
  }
  const ip = normalizeIP(raw)
  if (!ip || isUnusableIP(ip)) return null
  return ip
}

export function normalizeIP(raw: string): string | null {
  let value = raw.trim()
  if (!value) return null
  if (value.startsWith('[') && value.endsWith(']')) value = value.slice(1, -1)
  const zoneIndex = value.indexOf('%')
  if (zoneIndex >= 0) value = value.slice(0, zoneIndex)
  // 浏览器会把内网 host candidate 混淆成 mDNS 名，无法直接用于白名单。
  if (/\.local$/i.test(value)) return null

  const mapped = /^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/i.exec(value)
  if (mapped) value = mapped[1]

  if (IPV4_RE.test(value)) {
    const valid = value.split('.').every((part) => Number(part) <= 255)
    return valid ? value : null
  }
  if (value.includes(':') && IPV6_RE.test(value)) return value.toLowerCase()
  return null
}

function classifyIP(ip: string, candidateType: string): WebRTCIPScope {
  if (candidateType === 'srflx' || candidateType === 'prflx' || candidateType === 'relay') {
    return 'public'
  }
  return isPrivateIP(ip) ? 'local' : 'public'
}

function isPrivateIP(ip: string): boolean {
  if (IPV4_RE.test(ip)) {
    const [a, b] = ip.split('.').map(Number)
    if (a === 10) return true
    if (a === 172 && b >= 16 && b <= 31) return true
    if (a === 192 && b === 168) return true
    // 运营商级 NAT 段，和白名单里的“公网地址”不是同一回事。
    if (a === 100 && b >= 64 && b <= 127) return true
    return false
  }
  const lower = ip.toLowerCase()
  return lower.startsWith('fc') || lower.startsWith('fd')
}

function isUnusableIP(ip: string): boolean {
  if (IPV4_RE.test(ip)) {
    const [a, b] = ip.split('.').map(Number)
    if (a === 0 || a === 127) return true
    if (a === 169 && b === 254) return true
    if (a >= 224) return true
    return false
  }
  const lower = ip.toLowerCase()
  if (lower === '::' || lower === '::1') return true
  if (/^fe[89ab][0-9a-f]:/.test(lower)) return true
  if (lower.startsWith('ff')) return true
  return false
}

function sortAddresses(list: WebRTCIPAddress[]): WebRTCIPAddress[] {
  return list.sort((left, right) => {
    if (left.scope !== right.scope) return left.scope === 'public' ? -1 : 1
    const leftV4 = IPV4_RE.test(left.ip)
    const rightV4 = IPV4_RE.test(right.ip)
    if (leftV4 !== rightV4) return leftV4 ? -1 : 1
    return left.ip.localeCompare(right.ip)
  })
}
