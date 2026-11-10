import type { AdminListMediaResponse, AdminRemoveMediaResponse } from '@/core/api'
import type { MediaSort } from '@/core/realtime'
import { api } from '@/core/api'
import { toggleSelection } from '@/shared/lib/range-selection'
import { Store } from '@/shared/lib/store'
import { adminStore } from './admin-store'

export interface MediaQuery {
  q: string
  sort: MediaSort
  page: number
}

interface MediaState {
  // 最近读到的一页。换了参数之后、新的一页读到之前仍是上一页，这时 loading 为 true。
  list: AdminListMediaResponse | null
  // 读到 list 的时间，用来显示“几天前播放”。
  loadedAt: number
  loading: boolean
  // 最近一次读取失败的原因，给用户看。
  error: string | null
  // 只含这一页上的歌。
  selected: ReadonlySet<string>
}

// 读到的这一页是空的、又不是第 1 页时（删空了最后一页，或者打开了页码超出的旧链接），返回最后一页有内容的页码。
export function fallbackPage(list: AdminListMediaResponse): number | null {
  if (list.items.length || list.page <= 1)
    return null
  return Math.max(1, Math.ceil(list.total / list.pageSize))
}

// 每打开一次媒体页新建一个：离开再回来时重新读取。
export class MediaStore extends Store<MediaState> {
  #query: MediaQuery | null = null
  // 只有最后发起的读取能写入：换了参数、重试或删除之后，此前的读取作废。
  #generation = 0
  #anchor: string | null = null

  constructor() {
    super({ list: null, loadedAt: 0, loading: false, error: null, selected: new Set() })
  }

  // 地址里的参数变化时调用；参数和上一次相同时不重新读取。换了参数就清空选择。
  show(query: MediaQuery): void {
    const current = this.#query
    if (current && current.q === query.q && current.sort === query.sort && current.page === query.page)
      return
    this.#query = query
    this.clearSelection()
    void this.#load()
  }

  retry(): void {
    void this.#load()
  }

  toggle(key: string, shift: boolean): void {
    const keys = this.state.list?.items.map(item => item.key) ?? []
    const { selected, anchor } = toggleSelection(this.state.selected, keys, key, this.#anchor, shift)
    this.#anchor = anchor
    this.setState({ selected })
  }

  toggleAll(): void {
    const items = this.state.list?.items ?? []
    const all = items.every(item => this.state.selected.has(item.key))
    this.#anchor = null
    this.setState({ selected: new Set(all ? [] : items.map(item => item.key)) })
  }

  clearSelection(): void {
    this.#anchor = null
    this.setState({ selected: new Set() })
  }

  // 失败时原因已经通过通知显示，返回 null。无论成败都重新读取当前页，读完才返回：界面不会再显示已经删掉的歌，中途出错时出错之前删掉的也一样。
  async remove(keys: string[]): Promise<AdminRemoveMediaResponse | null> {
    const result = await adminStore.call(() => api.adminRemoveMedia({ media: keys.map(key => ({ key })) }))
    await this.#load()
    return result
  }

  async #load(): Promise<void> {
    const { q, sort, page } = this.#query!
    const generation = ++this.#generation
    this.setState({ loading: true, error: null })
    try {
      const list = await adminStore.attempt(() => api.adminListMedia({ page, sort, q }))
      if (generation !== this.#generation)
        return
      // 删除之后重新读到的这一页上，删掉的歌已经不在了，选择里也去掉。
      const present = new Set(list.items.map(item => item.key))
      if (this.#anchor !== null && !present.has(this.#anchor))
        this.#anchor = null
      this.setState({ list, loadedAt: Date.now(), loading: false, selected: new Set([...this.state.selected].filter(key => present.has(key))) })
    }
    catch (error) {
      if (generation === this.#generation)
        this.setState({ loading: false, error: (error as Error).message })
    }
  }
}
