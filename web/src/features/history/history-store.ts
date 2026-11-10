import type { SearchCategory, SearchRecord, SearchSource } from './records'
import type { IdentityEvent } from '@/core/api'
import type { MyListen, Track } from '@/core/realtime'
import { api, identityEvents } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { roomStore } from '@/core/room'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'
import { parseSearches, recordSearch } from './records'

export const searchLimits = [5, 10, 20, 50]
const defaultSearchLimit = 10

const searchesKey = 'alisten.search-history'
const searchesEnabledKey = 'alisten.search-history.enabled'
const searchLimitKey = 'alisten.search-history.limit'

export interface SearchSettings {
  searchesEnabled: boolean
  searchLimit: number
}

interface HistoryState extends SearchSettings {
  // 播放历史在服务端；还没有读到时为 null。
  listens: MyListen[] | null
  // 最近一次读取失败的原因，读取成功后为 null。
  error: string | null
  searches: SearchRecord[]
}

function loadSearchLimit(): number {
  const stored = Number(localStorage.getItem(searchLimitKey))
  return searchLimits.includes(stored) ? stored : defaultSearchLimit
}

function localDate(date: Date): string {
  return [date.getFullYear(), date.getMonth() + 1, date.getDate()].map(n => String(n).padStart(2, '0')).join('-')
}

function playingKey(): string {
  const nowPlaying = roomStore.state.room?.nowPlaying
  return nowPlaying ? `${nowPlaying.entry.id}@${nowPlaying.startedAtMs}` : ''
}

function sameTrack(a: Track, b: Track): boolean {
  return a.source === b.source && a.id === b.id
}

class HistoryStore extends Store<HistoryState> {
  #playing = ''
  #pages = 0
  // 只有最后发起的读取能写入：之后又读过一次，或者删除、清空、换了身份之后，此前的读取作废。
  #generation = 0
  #reading = 0

  constructor() {
    super({ listens: null, error: null, searches: [], searchesEnabled: true, searchLimit: defaultSearchLimit })
  }

  start(): void {
    const searchLimit = loadSearchLimit()
    const storedSearches = localStorage.getItem(searchesKey)
    const searches = parseSearches(storedSearches, searchLimit)
    this.setState({
      searches,
      searchesEnabled: localStorage.getItem(searchesEnabledKey) !== 'false',
      searchLimit,
    })
    if (storedSearches !== null && JSON.stringify(searches) !== storedSearches)
      this.#saveSearches(searches)
    this.#playing = playingKey()
    roomStore.addEventListener('change', this.#onRoom)
    window.addEventListener('storage', this.#onStorage)
    identityEvents.on(this.#onIdentity)
  }

  // 每打开一个历史页读取一次；页面开着时，服务端在开播时写入历史，所以正在播放的歌一变就重新读取。返回的函数在页面关闭时调用。
  open(): () => void {
    this.#pages++
    void this.load()
    let open = true
    return () => {
      if (open)
        this.#pages--
      open = false
    }
  }

  // 失败时只设置 error，不抛出。
  async load(): Promise<void> {
    const generation = ++this.#generation
    this.#reading++
    try {
      const { items } = await api.listMyListens()
      if (generation === this.#generation)
        this.setState({ listens: items, error: null })
    }
    catch (error) {
      if (generation === this.#generation)
        this.setState({ error: errorMessage(error) })
    }
    finally {
      this.#reading--
    }
  }

  // 返回 false 时，失败原因已经通过通知显示。
  async remove(listen: MyListen): Promise<boolean> {
    if (!await this.#write(() => api.removeMyListen({ track: listen.track })))
      return false
    this.#changed((this.state.listens ?? []).filter(l => !sameTrack(l.track, listen.track)))
    return true
  }

  // 返回 false 时，失败原因已经通过通知显示。
  async clear(): Promise<boolean> {
    if (!await this.#write(() => api.clearMyListens()))
      return false
    this.#changed([])
    return true
  }

  exportListens(): void {
    const now = new Date()
    const file = new Blob([JSON.stringify({ exportedAt: now.toISOString(), history: this.state.listens ?? [] }, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(file)
    const link = document.createElement('a')
    link.href = url
    link.download = `alisten-history-${localDate(now)}.json`
    link.click()
    // 下载在 click() 时已经开始，到下一个任务再释放对象 URL。
    setTimeout(() => URL.revokeObjectURL(url))
  }

  // 只记录用户发起的搜索（提交、点最近搜索、换来源和类别）；翻页、推荐、打开歌单，以及前进、后退还原的搜索都不计入。
  recordSearch(query: string, source: SearchSource, category: SearchCategory): void {
    if (this.state.searchesEnabled)
      this.#saveSearches(recordSearch(this.state.searches, { query, source, category, searchedAtMs: Date.now() }, this.state.searchLimit))
  }

  clearSearches(): void {
    this.#saveSearches([])
  }

  updateSettings({ searchesEnabled, searchLimit }: SearchSettings): void {
    localStorage.setItem(searchesEnabledKey, String(searchesEnabled))
    localStorage.setItem(searchLimitKey, String(searchLimit))
    this.setState({ searchesEnabled, searchLimit })
    if (this.state.searches.length > searchLimit)
      this.#saveSearches(this.state.searches.slice(0, searchLimit))
  }

  async #write(request: () => Promise<void>): Promise<boolean> {
    try {
      await request()
      return true
    }
    catch (error) {
      notificationStore.error(errorMessage(error))
      return false
    }
  }

  // 改动之前发起的读取可能带着改动前的历史返回，作废它们；有读取被作废时再读一次，免得错过其间开播的歌。
  #changed(listens: MyListen[]): void {
    this.#generation++
    this.setState({ listens })
    if (this.#reading > 0)
      void this.load()
  }

  #onRoom = (): void => {
    const playing = playingKey()
    if (playing === this.#playing)
      return
    this.#playing = playing
    if (playing && this.#pages > 0)
      void this.load()
  }

  // 成员 token 被拒绝时页面不重新加载：旧身份的历史不能留在页面上，新的游客还没有播放历史。新身份也可能是其他标签页
  // 换来的、已经有历史的游客，所以换好之后页面开着就重新读取。
  #onIdentity = (event: IdentityEvent): void => {
    if (event.kind === 'rejected') {
      this.#generation++
      this.setState({ listens: [], error: null })
    }
    else if (event.kind === 'renewed' && this.#pages > 0) {
      void this.load()
    }
  }

  // 其他标签页写入搜索记录时同步更新，本页之后的写入才不会覆盖它写入的记录。
  #onStorage = (event: StorageEvent): void => {
    if (event.key === searchesKey)
      this.setState({ searches: parseSearches(event.newValue, this.state.searchLimit) })
  }

  #saveSearches(searches: SearchRecord[]): void {
    localStorage.setItem(searchesKey, JSON.stringify(searches))
    this.setState({ searches })
  }
}

export const historyStore = new HistoryStore()
