import type { Source } from '@/core/realtime'
import type { SearchSource } from '@/features/history'
import type { SearchQuery } from '@/features/search'
import { canOpenPlaylists, defaultSearchSource, keywordSearch } from '@/features/search'

export type RoomPage = 'listen' | 'queue' | 'chat'
export type Panel = 'queue' | 'history' | 'chat'

// panel 是宽屏右侧的侧栏，和当前页面相同时不显示。
export type Route
  = | { name: 'lobby', filter: string, panel: Panel | null }
    | { name: 'admin', panel: Panel | null }
    | { name: 'settings', panel: Panel | null }
    | { name: 'history', filter: string, panel: Panel | null }
    | { name: RoomPage, ref: string, panel: Panel | null }
    | { name: 'search', ref: string, panel: Panel | null, query: SearchQuery }
    | { name: 'playlist', ref: string, panel: Panel | null, source: Source, id: string, page: number }

export type Page = Route['name']

function parseSource(value: string | null): SearchSource | null {
  return value === 'qq' || value === 'netease' ? value : null
}

function parsePanel(params: URLSearchParams): Panel | null {
  const panel = params.get('panel')
  return panel === 'queue' || panel === 'history' || panel === 'chat' ? panel : null
}

function parsePage(params: URLSearchParams): number {
  const page = Number(params.get('page'))
  return Number.isInteger(page) && page > 1 ? page : 1
}

function parseQuery(params: URLSearchParams): SearchQuery {
  const view = params.get('view')
  if (view === 'recommend')
    return { view }
  const keyword = params.get('q')?.trim() ?? ''
  const query = keywordSearch(keyword, parseSource(params.get('source')) ?? defaultSearchSource, view === 'tracks' || view === 'playlists' ? view : 'all')
  return keyword && query.view !== 'all' ? { ...query, page: parsePage(params) } : query
}

function writeQuery(query: SearchQuery, params: URLSearchParams): void {
  if (query.view === 'recommend') {
    params.set('view', 'recommend')
    return
  }
  if (query.keyword)
    params.set('q', query.keyword)
  // 有关键词时总是写出来源：以后默认来源再变，旧链接的含义也不变。
  if (query.keyword || query.source !== defaultSearchSource)
    params.set('source', query.source)
  if (query.view !== 'all')
    params.set('view', query.view)
  if (query.page > 1)
    params.set('page', String(query.page))
}

export function parseRoute({ pathname, search }: Pick<Location, 'pathname' | 'search'>): Route | null {
  const params = new URLSearchParams(search)
  const panel = parsePanel(params)
  if (pathname === '/')
    return { name: 'lobby', filter: params.get('q') ?? '', panel }
  if (pathname === '/admin')
    return { name: 'admin', panel }
  if (pathname === '/settings')
    return { name: 'settings', panel }
  if (pathname === '/history')
    return { name: 'history', filter: params.get('q') ?? '', panel }
  // 不做 decodeURIComponent：UUID、别名和歌单 ID 不含需要转义的字符，而它遇到不合法的转义序列会抛出异常。
  const room = /^\/r\/([^/]+)(\/.*)?$/.exec(pathname)
  if (!room)
    return null
  const [, ref, subpath = ''] = room
  switch (subpath) {
    case '':
      return { name: 'listen', ref, panel }
    case '/queue':
      return { name: 'queue', ref, panel }
    case '/chat':
      return { name: 'chat', ref, panel }
    case '/search':
      return { name: 'search', ref, panel, query: parseQuery(params) }
  }
  const playlist = /^\/playlists\/([^/]+)\/([^/]+)$/.exec(subpath)
  const source = parseSource(playlist?.[1] ?? null)
  if (!playlist || !source || !canOpenPlaylists(source))
    return null
  return { name: 'playlist', ref, panel, source, id: playlist[2], page: parsePage(params) }
}

function pathOf(route: Route): string {
  switch (route.name) {
    case 'lobby':
      return '/'
    case 'admin':
    case 'settings':
    case 'history':
      return `/${route.name}`
    case 'listen':
      return `/r/${route.ref}`
    case 'playlist':
      return `/r/${route.ref}/playlists/${route.source}/${route.id}`
    default:
      return `/r/${route.ref}/${route.name}`
  }
}

export function formatRoute(route: Route): string {
  const params = new URLSearchParams()
  if ((route.name === 'lobby' || route.name === 'history') && route.filter)
    params.set('q', route.filter)
  if (route.name === 'search')
    writeQuery(route.query, params)
  if (route.name === 'playlist' && route.page > 1)
    params.set('page', String(route.page))
  if (route.panel)
    params.set('panel', route.panel)
  const query = params.toString()
  return query ? `${pathOf(route)}?${query}` : pathOf(route)
}

// 换页面、进入另一个房间、提交新的关键词和打开另一个歌单写入新记录，后退一次回到上一个页面或上一次搜索；
// 同一个页面里换展示方式、开关侧栏和输入筛选词只替换当前地址。
export function isNewEntry(from: Route, to: Route): boolean {
  if (from.name !== to.name || ('ref' in from && 'ref' in to && from.ref !== to.ref))
    return true
  if (from.name === 'search' && to.name === 'search')
    return to.query.view !== 'recommend' && to.query.keyword !== (from.query.view === 'recommend' ? '' : from.query.keyword)
  if (from.name === 'playlist' && to.name === 'playlist')
    return to.source !== from.source || to.id !== from.id
  return false
}

// 取消进入房间时离开这个房间的地址：在它的页面上就后退，一直退到进入房间之前的页面；没有可以后退的记录（直接打开的房间链接）时换成发现房间；不在它的页面上就留在原处。
export function leaveStep(route: Route, leaving: ReadonlySet<string>, canBack: boolean): 'back' | 'lobby' | 'stay' {
  if (!('ref' in route) || !leaving.has(route.ref))
    return 'stay'
  return canBack ? 'back' : 'lobby'
}
