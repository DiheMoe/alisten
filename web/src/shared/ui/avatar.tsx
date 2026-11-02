import { useEffect, useState } from 'react'
import { Store } from '@/shared/lib/store'
import { useStore } from '@/shared/lib/use-store'

const gravatar = 'https://www.gravatar.com/avatar/'
const mirrors = ['https://weavatar.com/avatar/', 'https://cravatar.cn/avatar/']
const gravatarTimeoutMs = 3000
const mirrorTimeoutMs = 5000

// 有的网络访问不到 Gravatar，要改用镜像。头像加载失败分不清是没有头像（d=404）还是网络不通，所以另外探测：
// no-cors 请求拿到任何响应都算能访问。探测的地址要返回图片，其他类型的不透明响应可能被浏览器当作网络错误。
// 丢包的网络要等连接超时才会失败，所以限时。
function reachable(base: string, timeoutMs: number): Promise<string> {
  return fetch(`${base}?s=1&d=blank`, { mode: 'no-cors', signal: AbortSignal.timeout(timeoutMs) }).then(() => base)
}

// 镜像响应慢，逐个探测既慢又容易超时，所以同时探测、用先响应的那个；都不通时只显示首字。
class AvatarBase extends Store<{ url: string | null }> {
  #probing = false

  constructor() {
    super({ url: null })
  }

  probe(): void {
    if (this.#probing)
      return
    this.#probing = true
    void reachable(gravatar, gravatarTimeoutMs)
      .catch(() => Promise.any(mirrors.map(base => reachable(base, mirrorTimeoutMs))))
      .then(url => this.setState({ url }), () => {})
  }
}

const avatarBase = new AvatarBase()

export function Avatar({ hash, name, className = '' }: { hash: string, name: string, className?: string }) {
  const base = useStore(avatarBase, s => s.url)
  const [failedHash, setFailedHash] = useState<string>()
  useEffect(() => {
    if (hash)
      avatarBase.probe()
  }, [hash])
  return (
    <span className={`avatar ${className}`} aria-hidden="true">
      {base && hash && hash !== failedHash
        ? <img src={`${base}${hash}?s=96&d=404`} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => setFailedHash(hash)} />
        : Array.from(name.trim())[0] ?? '?'}
    </span>
  )
}
