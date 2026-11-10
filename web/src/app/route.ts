import type { MediaSort, Source } from '@/core/realtime'
import type { AdminSection } from '@/features/admin'
import type { SearchSource } from '@/features/history'
import type { SearchQuery } from '@/features/search'
import type { SettingsSection } from '@/features/settings'
import { canOpenPlaylists, defaultSearchSource, keywordSearch } from '@/features/search'

export type RoomPage = 'listen' | 'queue' | 'chat'
export type Panel = 'queue' | 'history' | 'chat'

// panel 是宽屏右侧的侧栏，和当前页面相同时不显示。
export type Route
  = | { name: 'lobby', filter: string, panel: Panel | null }
    // section 为 null 时地址是 /admin 或 /settings：宽屏显示第一个分类，窄屏显示分类列表。
    // q 和 page 只用于后台的房间和媒体，sort 只用于媒体。
    | { name: 'admin', section: AdminSection | null, panel: Panel | null, q: string, page: number, sort: MediaSort }
    | { name: 'settings', section: SettingsSection | null, panel: Panel | null }
    | { name: 'history', filter: string, panel: Panel | null }
    | { name: 'playlists', panel: Panel | null }
    | { name: 'my-playlist', id: string, panel: Panel | null }
    | { name: RoomPage, ref: string, panel: Panel | null }
    | { name: 'search', ref: string, panel: Panel | null, query: SearchQuery }
    | { name: 'playlist', ref: string, panel: Panel | null, source: Source, id: string, page: number }
    | { name: 'shared-playlist', shareId: string, panel: Panel | null }
    // 配对码在片段里，不进入请求和 Referer；写回地址时去掉，不留在历史记录中。
    | { name: 'pair', code: string, panel: Panel | null }
    // GitHub 授权之后的落地页，片段里是 code=<兑换码> 或 error=<错误码>，同样不留在地址中。
    | { name: 'github-login', fragment: string, panel: Panel | null }

export type Page = Route['name']

const settingsSections: Record<SettingsSection, true> = { account: true, appearance: true, playback: true, performance: true, privacy: true }
const adminSections: Record<AdminSection, true> = { site: true, registration: true, users: true, rooms: true, sources: true, media: true }
const mediaSorts: Record<MediaSort, true> = { stored: true, idle: true, size: true }
// 与接口对媒体搜索词的长度限制一致，按码点计：手改或截断的链接里更长的搜索词会让每次读取都失败。
const mediaQueryMaxLength = 100

function isOneOf<T extends string>(values: Record<T, true>, value: string): value is T {
  return Object.hasOwn(values, value)
}

function isPaged(section: AdminSection | null): boolean {
  return section === 'rooms' || section === 'media'
}

function parseSource(value: string | null): SearchSource | null {
  return value === 'qq' || value === 'netease' ? value : null
}

function parsePanel(params: URLSearchParams): Panel | null {
  const panel = params.get('panel')
  return panel === 'queue' || panel === 'history' || panel === 'chat' ? panel : null
}

// 超出安全整数的页码写回地址时会变成另一个数，接口也不接受：当作第 1 页。
function parsePage(params: URLSearchParams): number {
  const page = Number(params.get('page'))
  return Number.isSafeInteger(page) && page > 1 ? page : 1
}

function parseAdminQuery(section: AdminSection | null, params: URLSearchParams): string {
  const q = isPaged(section) ? params.get('q') ?? '' : ''
  return section === 'media' ? [...q].slice(0, mediaQueryMaxLength).join('') : q
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

export function parseRoute({ pathname, search, hash }: Pick<Location, 'pathname' | 'search' | 'hash'>): Route | null {
  const params = new URLSearchParams(search)
  const panel = parsePanel(params)
  if (pathname === '/')
    return { name: 'lobby', filter: params.get('q') ?? '', panel }
  const settings = /^\/settings(?:\/([^/]+))?$/.exec(pathname)
  if (settings) {
    const [, section = null] = settings
    return section === null || isOneOf(settingsSections, section) ? { name: 'settings', section, panel } : null
  }
  const admin = /^\/admin(?:\/([^/]+))?$/.exec(pathname)
  if (admin) {
    const [, section = null] = admin
    if (section !== null && !isOneOf(adminSections, section))
      return null
    const sort = params.get('sort') ?? ''
    return {
      name: 'admin',
      section,
      panel,
      q: parseAdminQuery(section, params),
      page: isPaged(section) ? parsePage(params) : 1,
      sort: section === 'media' && isOneOf(mediaSorts, sort) ? sort : 'stored',
    }
  }
  if (pathname === '/history')
    return { name: 'history', filter: params.get('q') ?? '', panel }
  if (pathname === '/playlists')
    return { name: 'playlists', panel }
  if (pathname === '/pair')
    return { name: 'pair', code: hash.slice(1), panel }
  if (pathname === '/auth/github')
    return { name: 'github-login', fragment: hash.slice(1), panel }
  const own = /^\/playlists\/([^/]+)$/.exec(pathname)
  if (own)
    return { name: 'my-playlist', id: own[1], panel }
  const shared = /^\/s\/([^/]+)$/.exec(pathname)
  if (shared)
    return { name: 'shared-playlist', shareId: shared[1], panel }
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
      return route.section ? `/${route.name}/${route.section}` : `/${route.name}`
    case 'history':
    case 'playlists':
      return `/${route.name}`
    case 'my-playlist':
      return `/playlists/${route.id}`
    case 'pair':
      return '/pair'
    case 'github-login':
      return '/auth/github'
    case 'listen':
      return `/r/${route.ref}`
    case 'playlist':
      return `/r/${route.ref}/playlists/${route.source}/${route.id}`
    case 'shared-playlist':
      return `/s/${route.shareId}`
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
  if (route.name === 'admin' && isPaged(route.section)) {
    if (route.q)
      params.set('q', route.q)
    if (route.section === 'media' && route.sort !== 'stored')
      params.set('sort', route.sort)
    if (route.page > 1)
      params.set('page', String(route.page))
  }
  if (route.panel)
    params.set('panel', route.panel)
  const query = params.toString()
  return query ? `${pathOf(route)}?${query}` : pathOf(route)
}

// 换页面、进入另一个房间、换设置或后台的分类、提交新的关键词和打开另一个歌单写入新记录，后退一次回到上一个页面、分类或上一次搜索；
// 同一个页面里换展示方式、开关侧栏、输入筛选词、排序和翻页只替换当前地址。
export function isNewEntry(from: Route, to: Route): boolean {
  if (from.name !== to.name || ('ref' in from && 'ref' in to && from.ref !== to.ref))
    return true
  if ((from.name === 'settings' && to.name === 'settings') || (from.name === 'admin' && to.name === 'admin'))
    return from.section !== to.section
  if (from.name === 'my-playlist' && to.name === 'my-playlist')
    return from.id !== to.id
  if (from.name === 'search' && to.name === 'search')
    return to.query.view !== 'recommend' && to.query.keyword !== (from.query.view === 'recommend' ? '' : from.query.keyword)
  if (from.name === 'playlist' && to.name === 'playlist')
    return to.source !== from.source || to.id !== from.id
  if (from.name === 'shared-playlist' && to.name === 'shared-playlist')
    return from.shareId !== to.shareId
  return false
}

// 从设置或后台的分类回到分类列表：上一条记录就是列表时后退，否则把当前记录换成列表。两种都不新增记录，返回键不会再回到刚离开的分类。
export function upStep(list: Route, from: string | undefined, canBack: boolean): 'back' | 'replace' {
  return canBack && from === formatRoute(list) ? 'back' : 'replace'
}

// 取消进入房间时离开这个房间的地址：在它的页面上就后退，一直退到进入房间之前的页面；没有可以后退的记录（直接打开的房间链接）时换成发现房间；不在它的页面上就留在原处。
export function leaveStep(route: Route, leaving: ReadonlySet<string>, canBack: boolean): 'back' | 'lobby' | 'stay' {
  if (!('ref' in route) || !leaving.has(route.ref))
    return 'stay'
  return canBack ? 'back' : 'lobby'
}
