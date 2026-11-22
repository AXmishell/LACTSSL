#!/usr/bin/env node
// 从发布签名私钥派生可嵌入二进制的 Ed25519 原始公钥（base64）。
//
// 用法:
//   ED25519_PRIVATE_KEY_B64=<base64 PKCS#8 DER> node scripts/derive-program-public-key.mjs
//
// 输出为 32 字节原始公钥 base64，可直接传给：
//   -ldflags "-X 'lactssl/internal/config.OfficialProgramPublicKey=<输出>'"
//
// 也可以通过 LACTSSL_PROGRAM_PUBLIC_KEY 直接指定公钥，脚本会原样输出。
import { createPrivateKey, createPublicKey } from 'node:crypto'

const direct = (process.env.LACTSSL_PROGRAM_PUBLIC_KEY || '').trim()
if (direct) {
  process.stdout.write(direct)
  process.exit(0)
}

const encoded = (process.env.ED25519_PRIVATE_KEY_B64 || '').trim()
if (!encoded) {
  // 预发布或受控环境可以没有签名密钥；输出空串表示不嵌入。
  process.exit(0)
}

let privateKey
try {
  privateKey = createPrivateKey({
    key: Buffer.from(encoded, 'base64'),
    format: 'der',
    type: 'pkcs8'
  })
} catch (error) {
  console.error('ED25519_PRIVATE_KEY_B64 不是有效的 PKCS#8 DER 私钥: ' + error.message)
  process.exit(1)
}
if (privateKey.asymmetricKeyType !== 'ed25519') {
  console.error('ED25519_PRIVATE_KEY_B64 不是 Ed25519 私钥')
  process.exit(1)
}

const spki = createPublicKey(privateKey).export({ type: 'spki', format: 'der' })
if (spki.length < 32) {
  console.error('公钥 DER 长度异常: ' + spki.length)
  process.exit(1)
}
// Ed25519 SPKI DER 的前 12 字节是固定头，原始公钥是最后 32 字节。
process.stdout.write(spki.subarray(spki.length - 32).toString('base64'))
