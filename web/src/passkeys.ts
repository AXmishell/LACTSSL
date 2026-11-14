import { api } from './api'
import type { AuthResult, PasskeyInfo } from './types'

// WebAuthn 的二进制字段使用 base64url 传输，浏览器 API 需要 ArrayBuffer。
function base64urlToBytes(value: string): Uint8Array {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4)
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}

function bytesToBase64url(value: ArrayBuffer | ArrayBufferView | null): string {
  if (!value) return ''
  const bytes = value instanceof ArrayBuffer ? new Uint8Array(value) : new Uint8Array(value.buffer, value.byteOffset, value.byteLength)
  let binary = ''
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i])
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function parseCreationOptions(raw: any): PublicKeyCredentialCreationOptions {
  const options = raw?.publicKey ?? raw
  if (!options) throw new Error('invalid passkey options')
  return {
    ...options,
    challenge: base64urlToBytes(options.challenge),
    user: { ...options.user, id: base64urlToBytes(options.user.id) },
    excludeCredentials: (options.excludeCredentials || []).map((item: any) => ({
      ...item,
      id: base64urlToBytes(item.id)
    }))
  } as PublicKeyCredentialCreationOptions
}

function parseRequestOptions(raw: any): PublicKeyCredentialRequestOptions {
  const options = raw?.publicKey ?? raw
  if (!options) throw new Error('invalid passkey options')
  return {
    ...options,
    challenge: base64urlToBytes(options.challenge),
    allowCredentials: (options.allowCredentials || []).map((item: any) => ({
      ...item,
      id: base64urlToBytes(item.id)
    }))
  } as PublicKeyCredentialRequestOptions
}

// 浏览器返回的是 ArrayBuffer，需要显式序列化后交给后端校验。
// 直接 JSON.stringify(credential) 会得到空对象。
export function serializeCredential(credential: PublicKeyCredential): Record<string, any> {
  const response: any = credential.response
  const result: Record<string, any> = {
    id: credential.id,
    rawId: bytesToBase64url(credential.rawId),
    type: credential.type,
    clientExtensionResults: credential.getClientExtensionResults?.() ?? {}
  }
  if ('attestationObject' in response) {
    result.response = {
      clientDataJSON: bytesToBase64url(response.clientDataJSON),
      attestationObject: bytesToBase64url(response.attestationObject),
      transports: response.getTransports?.() ?? []
    }
  } else {
    result.response = {
      clientDataJSON: bytesToBase64url(response.clientDataJSON),
      authenticatorData: bytesToBase64url(response.authenticatorData),
      signature: bytesToBase64url(response.signature),
      userHandle: response.userHandle ? bytesToBase64url(response.userHandle) : null
    }
  }
  return result
}

export function passkeySupported(): boolean {
  return typeof window !== 'undefined' && !!window.PublicKeyCredential && !!navigator.credentials
}

export function passkeySecureContext(): boolean {
  if (typeof window === 'undefined') return false
  if (window.isSecureContext) return true
  return location.hostname === 'localhost' || location.hostname === '127.0.0.1' || location.hostname === '[::1]'
}

export function isPasskeyCancelled(error: unknown): boolean {
  return (
    typeof DOMException !== 'undefined' &&
    error instanceof DOMException &&
    (error.name === 'NotAllowedError' || error.name === 'AbortError')
  )
}

interface CeremonyResponse {
  session_id: string
  options: any
}

export async function loginWithPasskey(): Promise<AuthResult> {
  if (!passkeySupported()) throw new Error('passkey unsupported')
  const begin = await api.post<CeremonyResponse>('/auth/passkey/login/begin')
  const credential = (await navigator.credentials.get({
    publicKey: parseRequestOptions(begin.options)
  })) as PublicKeyCredential | null
  if (!credential) throw new Error('passkey cancelled')
  return api.post<AuthResult>('/auth/passkey/login/finish', {
    session_id: begin.session_id,
    credential: serializeCredential(credential)
  })
}

export async function registerPasskey(name: string, password: string): Promise<PasskeyInfo> {
  if (!passkeySupported()) throw new Error('passkey unsupported')
  const begin = await api.post<CeremonyResponse>('/auth/passkeys/register/begin', { password })
  const credential = (await navigator.credentials.create({
    publicKey: parseCreationOptions(begin.options)
  })) as PublicKeyCredential | null
  if (!credential) throw new Error('passkey cancelled')
  return api.post<PasskeyInfo>('/auth/passkeys/register/finish', {
    session_id: begin.session_id,
    name,
    credential: serializeCredential(credential)
  })
}

// requestPasskeyProof 完成一次 WebAuthn 断言，换取短期二次验证证明。
export async function requestPasskeyProof(): Promise<string> {
  if (!passkeySupported()) throw new Error('passkey unsupported')
  const begin = await api.post<CeremonyResponse>('/auth/passkey/verify/begin')
  const credential = (await navigator.credentials.get({
    publicKey: parseRequestOptions(begin.options)
  })) as PublicKeyCredential | null
  if (!credential) throw new Error('passkey cancelled')
  const result = await api.post<{ proof: string }>('/auth/passkey/verify/finish', {
    session_id: begin.session_id,
    credential: serializeCredential(credential)
  })
  return result.proof
}
