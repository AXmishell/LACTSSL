#!/usr/bin/env node
// 为发布校验和生成 Ed25519 分离签名。
//
// 用法:
//   ED25519_PRIVATE_KEY_B64=<base64 PKCS#8 DER> \
//     node scripts/sign-checksums.mjs dist/checksums.txt dist/checksums.txt.sig
//
// 生成密钥（一次性，私钥写入 GitHub Actions Secret）:
//   node -e 'const{generateKeyPairSync}=require("node:crypto");const{publicKey,privateKey}=generateKeyPairSync("ed25519");console.log("PUBLIC="+publicKey.export({type:"spki",format:"der"}).toString("base64"));console.log("PRIVATE="+privateKey.export({type:"pkcs8",format:"der"}).toString("base64"))'
//
// 主程序侧把 PUBLIC 对应的原始 32 字节公钥（base64）写入
// config.yaml 的 upgrade.program.public_key。
import { readFileSync, writeFileSync } from 'node:fs'
import { createPrivateKey, sign } from 'node:crypto'

const [source = 'checksums.txt', target = 'checksums.txt.sig'] = process.argv.slice(2)
const encoded = (process.env.ED25519_PRIVATE_KEY_B64 || '').trim()
if (!encoded) {
  console.error('未配置 ED25519_PRIVATE_KEY_B64，无法签名')
  process.exit(1)
}

let key
try {
  key = createPrivateKey({
    key: Buffer.from(encoded, 'base64'),
    format: 'der',
    type: 'pkcs8'
  })
} catch (error) {
  console.error('ED25519_PRIVATE_KEY_B64 不是有效的 PKCS#8 DER 私钥: ' + error.message)
  process.exit(1)
}
if (key.asymmetricKeyType !== 'ed25519') {
  console.error('ED25519_PRIVATE_KEY_B64 不是 Ed25519 私钥')
  process.exit(1)
}

// Ed25519 签名不需要指定摘要算法，Node 会按密钥类型选择。
const signature = sign(null, readFileSync(source), key)
writeFileSync(target, signature.toString('base64') + '\n')
console.log(`已写入 ${target}`)
