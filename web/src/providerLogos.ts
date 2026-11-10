import alidns from './assets/providers/alidns.svg'
import cloudflare from './assets/providers/cloudflare.svg'
import digitalocean from './assets/providers/digitalocean.svg'
import dnspod from './assets/providers/dnspod.svg'
import gandi from './assets/providers/gandi.svg'
import godaddy from './assets/providers/godaddy.svg'
import hetzner from './assets/providers/hetzner.svg'
import linode from './assets/providers/linode.svg'
import namecheap from './assets/providers/namecheap.svg'
import qiniu from './assets/providers/qiniu.png'
import tencentcloud from './assets/providers/tencentcloud.svg'
import vultr from './assets/providers/vultr.svg'

// 上游服务商官方 logo，用于凭据列表头像；键与后端 ProviderDef.Value 保持一致。
const providerLogos: Record<string, string> = {
  alidns,
  cloudflare,
  dnspod,
  tencentcloud,
  digitalocean,
  godaddy,
  namecheap,
  vultr,
  linode,
  hetzner,
  gandi,
  aliyun_cdn: alidns,
  qiniu_cdn: qiniu,
  qiniu_kodo: qiniu
}

export function providerLogo(value?: string): string {
  return (value && providerLogos[value]) || ''
}
