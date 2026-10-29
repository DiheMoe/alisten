import type { ErrorTexts } from '@/core/errors'
import type { PlaylistSummary, Source, TrackSummary } from '@/core/realtime'
import type { SearchCategory, SearchSource } from '@/features/history'
import { errorMessage } from '@/core/errors'
import { roomStore } from '@/core/room'
import { historyStore } from '@/features/history'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'

export type SearchView = SearchCategory | 'recommend'

export interface KeywordQuery {
  view: SearchCategory
  keyword: string
  source: SearchSource
  page: number
}

export type SearchQuery = { view: 'recommend' } | KeywordQuery

interface OpenPlaylist {
  source: Source
  id: string
}

export const pageSize = 20
export const defaultSearchSource: SearchSource = 'qq'
const cacheLimit = 10
const cacheTtlMs = 10 * 60 * 1000
// 搜索框最多输入 500 个字符，但地址里的关键词可能更长，超过时服务端返回 INVALID_ARGUMENT。
const keywordTexts: ErrorTexts = { INVALID_ARGUMENT: '关键词太长了，最多 500 个字符。' }

interface Cached<T> {
  value: T
  at: number
}

interface Results {
  tracks: TrackSummary[]
  tracksTotal: number
  playlists: PlaylistSummary[]
  playlistsTotal: number
  recommendations: TrackSummary[]
}

interface SearchState extends Results {
  text: string
  // 当前结果对应的关键词；为空时页面显示最近搜索。
  keyword: string
  source: SearchSource
  view: SearchView
  page: number
  // 打开了歌单时，结果是歌单内的歌曲。
  playlist: OpenPlaylist | null
  loading: boolean
  failed: boolean
}

const noResults: Results = { tracks: [], tracksTotal: 0, playlists: [], playlistsTotal: 0, recommendations: [] }

// 只有网易云能取歌单内的歌曲，其他平台的歌单搜到了也打不开，所以不搜。
export function canOpenPlaylists(source: Source): boolean {
  return source === 'netease'
}

export function keywordSearch(keyword: string, source: SearchSource, category: SearchCategory, page = 1): KeywordQuery {
  return { view: category === 'playlists' && !canOpenPlaylists(source) ? 'all' : category, keyword, source, page }
}

// 用户发起的搜索记入搜索历史：提交、点最近搜索、换来源和类别；翻页、推荐、打开歌单，以及前进、后退还原的搜索都不记。
export function recordSearch(query: KeywordQuery): void {
  if (query.keyword)
    historyStore.recordSearch(query.keyword, query.source, query.view)
}

function sameQuery(a: SearchQuery, b: SearchQuery): boolean {
  if (a.view === 'recommend' || b.view === 'recommend')
    return a.view === b.view
  return a.view === b.view && a.keyword === b.keyword && a.source === b.source && a.page === b.page
}

// shown 是当前结果对应的条件。缓存里有就直接用，不必连着房间；连上房间之前不发出请求：推迟时让当前结果作废，之后回到哪个条件都会重新取。
export function nextLoad(key: string | null, shown: string | null, cached: boolean, connected: boolean, force: boolean): { shown: string | null, loading: boolean, from: 'none' | 'cache' | 'request' } | null {
  if (key === null)
    return { shown: null, loading: false, from: 'none' }
  if (key === shown && !force)
    return null
  if (cached && !force)
    return { shown: key, loading: false, from: 'cache' }
  if (!connected)
    return { shown: null, loading: true, from: 'none' }
  return { shown: key, loading: true, from: 'request' }
}

// 取回超过 10 分钟的结果不再使用。
export function cachedResult<T>(cache: ReadonlyMap<string, Cached<T>>, key: string, now: number): Cached<T> | null {
  const entry = cache.get(key)
  return entry && now - entry.at < cacheTtlMs ? entry : null
}

// 记下刚显示的结果：放到最近，去掉已经失效的，超过 10 份时丢掉最久没显示的。
export function rememberResult<T>(cache: ReadonlyMap<string, Cached<T>>, key: string, entry: Cached<T>, now: number): Map<string, Cached<T>> {
  const kept: [string, Cached<T>][] = [...cache].filter(([other, { at }]) => other !== key && now - at < cacheTtlMs)
  return new Map([...kept, [key, entry] as [string, Cached<T>]].slice(-cacheLimit))
}

// 显示什么由地址决定：页面只把新的条件交给导航，地址变化之后再由 show() 和 showPlaylist() 取结果。
class SearchStore extends Store<SearchState> {
  #request = 0
  #roomId: string | null = null
  #shown: string | null = null
  #cache: ReadonlyMap<string, Cached<Partial<Results>>> = new Map()
  // 只有显示着搜索页或歌单页时才请求，连上房间时也只为它们补发。
  #visible = false

  constructor() {
    super({ ...noResults, text: '', keyword: '', source: defaultSearchSource, view: 'all', page: 1, playlist: null, loading: false, failed: false })
  }

  start(): void {
    roomStore.addEventListener('change', this.#onRoom)
  }

  get query(): SearchQuery {
    const { view, keyword, source, page } = this.state
    return view === 'recommend' ? { view } : { view, keyword, source, page }
  }

  setText(text: string): void {
    this.setState({ text })
  }

  // 新的关键词沿用当前的来源和类别。
  withKeyword(keyword: string): KeywordQuery {
    const { source, view } = this.state
    return keywordSearch(keyword, source, view === 'recommend' ? 'all' : view)
  }

  show(query: SearchQuery): void {
    // 地址里的条件变了，搜索框的文字就跟着地址里的关键词，没有关键词时清空；只是开关侧栏、关掉弹窗时不动正在输入的文字。
    const changed = !this.#visible || this.state.playlist !== null || !sameQuery(this.query, query)
    this.#visible = true
    const keyword = query.view === 'recommend' ? '' : query.keyword
    const text = changed ? keyword : this.state.text
    if (query.view === 'recommend')
      this.setState({ view: 'recommend', keyword, page: 1, playlist: null, text })
    else
      this.setState({ ...query, playlist: null, text })
    void this.#load()
  }

  showPlaylist(source: Source, id: string): void {
    this.#visible = true
    const { playlist } = this.state
    if (playlist?.source !== source || playlist.id !== id)
      this.setState({ playlist: { source, id } })
    void this.#load()
  }

  hide(): void {
    this.#visible = false
  }

  retry(): void {
    void this.#load(true)
  }

  #key(): string | null {
    const { keyword, source, view, page, playlist } = this.state
    if (playlist)
      return `playlist ${playlist.source} ${playlist.id}`
    if (view === 'recommend')
      return `recommend ${roomStore.state.roomId}`
    return keyword ? JSON.stringify([keyword, source, view, page]) : null
  }

  async #load(force = false): Promise<void> {
    const key = this.#key()
    const now = Date.now()
    const hit = key === null ? null : cachedResult(this.#cache, key, now)
    const next = nextLoad(key, this.#shown, hit !== null, roomStore.state.status === 'open', force)
    if (!next)
      return
    this.#shown = next.shown
    const request = ++this.#request
    if (key !== null && hit !== null && next.from === 'cache') {
      this.#cache = rememberResult(this.#cache, key, hit, now)
      this.setState({ ...hit.value, loading: false, failed: false })
      return
    }
    this.setState({ loading: next.loading, failed: false })
    if (key === null || next.from !== 'request')
      return
    const { view, playlist } = this.state
    const texts = playlist || view === 'recommend' ? undefined : keywordTexts
    const outcomes = await Promise.allSettled(this.#requests())
    if (request !== this.#request)
      return
    let results: Partial<Results> = view === 'recommend' && !playlist ? { recommendations: [] } : { tracks: [], tracksTotal: 0, playlists: [], playlistsTotal: 0 }
    const failures = new Set<string>()
    for (const outcome of outcomes) {
      if (outcome.status === 'fulfilled')
        results = { ...results, ...outcome.value }
      else
        failures.add(errorMessage(outcome.reason, texts))
    }
    for (const message of failures)
      notificationStore.error(message)
    if (!failures.size) {
      const at = Date.now()
      this.#cache = rememberResult(this.#cache, key, { value: results, at }, at)
    }
    this.setState({ ...results, loading: false, failed: failures.size > 0 })
  }

  #requests(): Promise<Partial<Results>>[] {
    const { keyword, source, view, playlist, page } = this.state
    const { requests } = roomStore
    if (playlist)
      return [requests.playlistTracks({ source: playlist.source, playlistId: playlist.id }).then(({ items, total }) => ({ tracks: items, tracksTotal: total }))]
    if (view === 'recommend')
      return [requests.recommendList().then(({ items }) => ({ recommendations: items }))]
    const tracks = (at: number) => requests.searchTracks({ source, keyword, page: at, pageSize }).then(({ items, total }) => ({ tracks: items, tracksTotal: total }))
    const playlists = (at: number) => requests.searchPlaylists({ source, keyword, page: at, pageSize }).then(({ items, total }) => ({ playlists: items, playlistsTotal: total }))
    if (view === 'all')
      return canOpenPlaylists(source) ? [tracks(1), playlists(1)] : [tracks(1)]
    return [view === 'tracks' ? tracks(page) : playlists(page)]
  }

  // 结果和缓存只属于当前房间，换了房间都作废（推荐由房间的播放历史得出）；连上房间时补发还没有发出的请求。
  #onRoom = (): void => {
    const { roomId, status } = roomStore.state
    if (roomId !== this.#roomId) {
      this.#roomId = roomId
      this.#cache = new Map()
      this.#shown = null
      ++this.#request
      if (this.state.view === 'recommend' && !this.state.playlist)
        this.setState({ recommendations: [], loading: false, failed: false })
      else
        this.setState({ recommendations: [] })
    }
    if (status === 'open' && this.#visible)
      void this.#load()
  }
}

export const searchStore = new SearchStore()
