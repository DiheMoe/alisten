import { api } from '@/core/api'
import { Store } from '@/shared/lib/store'

const nameKey = 'alisten.brand-name'
const defaultName = 'Alisten'

class BrandStore extends Store<{ name: string }> {
  // 只采用最后一次读取或保存带回的名字：先发出的读取可能在后台改名之前就读到了旧的名字。
  #requests = 0

  constructor() {
    super({ name: defaultName })
  }

  // 先用这台设备记下的品牌名，打开页面时才不会先闪一下默认的名字；取不到最新的品牌名时沿用它，不提示。
  start(): void {
    this.setState({ name: localStorage.getItem(nameKey) || defaultName })
    void this.load().catch(() => {})
  }

  async load(): Promise<void> {
    const request = ++this.#requests
    const { brandName } = await api.getSite()
    if (request === this.#requests)
      this.#remember(brandName)
  }

  setName(name: string): void {
    ++this.#requests
    this.#remember(name)
  }

  #remember(name: string): void {
    localStorage.setItem(nameKey, name)
    this.setState({ name })
  }
}

export const brandStore = new BrandStore()
