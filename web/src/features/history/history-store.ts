import type { PlayRecord, SearchCategory, SearchRecord, SearchSource } from './records'
import { roomStore } from '@/core/room'
import { Store } from '@/shared/lib/store'
import { parsePlays, parseSearches, recordPlay, recordSearch } from './records'

export const searchLimits = [5, 10, 20, 50]
const defaultSearchLimit = 10

const playsKey = 'alisten.play-history'
const playsEnabledKey = 'alisten.play-history.enabled'
const searchesKey = 'alisten.search-history'
const searchesEnabledKey = 'alisten.search-history.enabled'
const searchLimitKey = 'alisten.search-history.limit'

export interface HistorySettings {
  playsEnabled: boolean
  searchesEnabled: boolean
  searchLimit: number
}

interface HistoryState extends HistorySettings {
  plays: PlayRecord[]
  searches: SearchRecord[]
}

function loadSearchLimit(): number {
  const stored = Number(localStorage.getItem(searchLimitKey))
  return searchLimits.includes(stored) ? stored : defaultSearchLimit
}

function localDate(date: Date): string {
  return [date.getFullYear(), date.getMonth() + 1, date.getDate()].map(n => String(n).padStart(2, '0')).join('-')
}

class HistoryStore extends Store<HistoryState> {
  #playing = ''

  constructor() {
    super({ plays: [], playsEnabled: true, searches: [], searchesEnabled: true, searchLimit: defaultSearchLimit })
  }

  start(): void {
    const searchLimit = loadSearchLimit()
    const storedSearches = localStorage.getItem(searchesKey)
    const searches = parseSearches(storedSearches, searchLimit)
    this.setState({
      plays: parsePlays(localStorage.getItem(playsKey)),
      playsEnabled: localStorage.getItem(playsEnabledKey) !== 'false',
      searches,
      searchesEnabled: localStorage.getItem(searchesEnabledKey) !== 'false',
      searchLimit,
    })
    if (storedSearches !== null && JSON.stringify(searches) !== storedSearches)
      this.#saveSearches(searches)
    roomStore.addEventListener('change', this.#onRoom)
    window.addEventListener('storage', this.#onStorage)
  }

  removePlay(record: PlayRecord): void {
    this.#savePlays(this.state.plays.filter(play => play !== record))
  }

  clearPlays(): void {
    this.#savePlays([])
  }

  exportPlays(): void {
    const now = new Date()
    const file = new Blob([JSON.stringify({ exportedAt: now.toISOString(), history: this.state.plays }, null, 2)], { type: 'application/json' })
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

  updateSettings({ playsEnabled, searchesEnabled, searchLimit }: HistorySettings): void {
    localStorage.setItem(playsEnabledKey, String(playsEnabled))
    localStorage.setItem(searchesEnabledKey, String(searchesEnabled))
    localStorage.setItem(searchLimitKey, String(searchLimit))
    this.setState({ playsEnabled, searchesEnabled, searchLimit })
    if (this.state.searches.length > searchLimit)
      this.#saveSearches(this.state.searches.slice(0, searchLimit))
  }

  #onRoom = (): void => {
    const nowPlaying = roomStore.state.room?.nowPlaying ?? null
    const playing = nowPlaying ? `${nowPlaying.entry.id}@${nowPlaying.startedAtMs}` : ''
    if (playing === this.#playing)
      return
    this.#playing = playing
    if (nowPlaying && this.state.playsEnabled)
      this.#savePlays(recordPlay(this.state.plays, { track: nowPlaying.entry.track, meta: nowPlaying.entry.meta, playedAtMs: nowPlaying.startedAtMs }))
  }

  // 其他标签页写入历史时同步更新，本页之后的写入才不会覆盖它写入的记录。
  #onStorage = (event: StorageEvent): void => {
    if (event.key === playsKey)
      this.setState({ plays: parsePlays(event.newValue) })
    else if (event.key === searchesKey)
      this.setState({ searches: parseSearches(event.newValue, this.state.searchLimit) })
  }

  #savePlays(plays: PlayRecord[]): void {
    localStorage.setItem(playsKey, JSON.stringify(plays))
    this.setState({ plays })
  }

  #saveSearches(searches: SearchRecord[]): void {
    localStorage.setItem(searchesKey, JSON.stringify(searches))
    this.setState({ searches })
  }
}

export const historyStore = new HistoryStore()
