import type { Route } from './route'
import type { MediaSort } from '@/core/realtime'
import type { AdminSection } from '@/features/admin'
import type { SearchQuery } from '@/features/search'
import { describe, expect, it } from 'vitest'
import { formatRoute, isNewEntry, leaveStep, parseRoute, upStep } from './route'

function parse(url: string): Route | null {
  return parseRoute(new URL(url, 'https://alisten.test'))
}

describe('parseRoute', () => {
  it.each<[string, Route]>([
    ['/', { name: 'lobby', filter: '', panel: null }],
    ['/?q=+%E5%A4%9C&panel=chat', { name: 'lobby', filter: ' 夜', panel: 'chat' }],
    ['/admin', { name: 'admin', section: null, panel: null, q: '', page: 1, sort: 'stored' }],
    ['/admin/site', { name: 'admin', section: 'site', panel: null, q: '', page: 1, sort: 'stored' }],
    ['/admin/rooms?q=jazz&page=2&panel=chat', { name: 'admin', section: 'rooms', panel: 'chat', q: 'jazz', page: 2, sort: 'stored' }],
    ['/admin/media?q=lemon&sort=size&page=3', { name: 'admin', section: 'media', panel: null, q: 'lemon', page: 3, sort: 'size' }],
    ['/settings?panel=queue', { name: 'settings', section: null, panel: 'queue' }],
    ['/settings/privacy', { name: 'settings', section: 'privacy', panel: null }],
    ['/history', { name: 'history', filter: '', panel: null }],
    ['/history?q=%E6%99%B4&panel=history', { name: 'history', filter: '晴', panel: 'history' }],
    ['/playlists', { name: 'playlists', panel: null }],
    ['/playlists/abc', { name: 'my-playlist', id: 'abc', panel: null }],
    ['/playlists/abc?panel=queue', { name: 'my-playlist', id: 'abc', panel: 'queue' }],
    ['/r/my-room', { name: 'listen', ref: 'my-room', panel: null }],
    ['/r/my-room?panel=history', { name: 'listen', ref: 'my-room', panel: 'history' }],
    ['/r/my-room/queue?panel=queue', { name: 'queue', ref: 'my-room', panel: 'queue' }],
    ['/r/my-room/chat', { name: 'chat', ref: 'my-room', panel: null }],
    ['/r/my-room/search', { name: 'search', ref: 'my-room', panel: null, query: { view: 'all', keyword: '', source: 'qq', page: 1 } }],
    ['/r/my-room/search?q=%E6%99%B4%E5%A4%A9&source=netease&view=playlists&page=2&panel=chat', { name: 'search', ref: 'my-room', panel: 'chat', query: { view: 'playlists', keyword: '晴天', source: 'netease', page: 2 } }],
    ['/r/my-room/search?source=netease&view=tracks', { name: 'search', ref: 'my-room', panel: null, query: { view: 'tracks', keyword: '', source: 'netease', page: 1 } }],
    ['/r/my-room/search?view=recommend', { name: 'search', ref: 'my-room', panel: null, query: { view: 'recommend' } }],
    ['/r/my-room/search?q=a&source=qq&view=playlists', { name: 'search', ref: 'my-room', panel: null, query: { view: 'playlists', keyword: 'a', source: 'qq', page: 1 } }],
    ['/r/my-room/playlists/netease/123?page=3&panel=queue', { name: 'playlist', ref: 'my-room', panel: 'queue', source: 'netease', id: '123', page: 3 }],
    ['/r/my-room/playlists/qq/123', { name: 'playlist', ref: 'my-room', panel: null, source: 'qq', id: '123', page: 1 }],
    ['/s/AbC_-1x', { name: 'shared-playlist', shareId: 'AbC_-1x', panel: null }],
    ['/s/AbC_-1x?panel=queue', { name: 'shared-playlist', shareId: 'AbC_-1x', panel: 'queue' }],
  ])('%s', (url, route) => {
    expect(parse(url)).toEqual(route)
    expect(formatRoute(route)).toBe(url)
  })

  it.each([
    ['/admin?from=menu', '/admin'],
    ['/admin/users?q=a&page=2&sort=size', '/admin/users'],
    ['/admin/rooms?sort=size', '/admin/rooms'],
    ['/admin/media?sort=idle&page=1', '/admin/media?sort=idle'],
    ['/settings?panel=lyrics', '/settings'],
    ['/?q=', '/'],
    ['/r/my-room?panel=lyrics', '/r/my-room'],
    ['/r/my-room/search?q=%20a%20', '/r/my-room/search?q=a&source=qq'],
    ['/r/my-room/search?q=a&source=kuwo&view=lyrics&page=0', '/r/my-room/search?q=a&source=qq'],
    ['/r/my-room/search?q=a&page=2', '/r/my-room/search?q=a&source=qq'],
    ['/r/my-room/search?view=tracks&page=2', '/r/my-room/search?view=tracks'],
    ['/r/my-room/search?view=recommend&q=a&source=netease&page=2', '/r/my-room/search?view=recommend'],
    ['/r/my-room/playlists/netease/123?page=1.5', '/r/my-room/playlists/netease/123'],
    ['/admin/media?page=99999999999999999999', '/admin/media'],
    ['/admin/media?page=1e300', '/admin/media'],
    ['/admin/media?page=9007199254740991', '/admin/media?page=9007199254740991'],
    ['/r/my-room/playlists/netease/123?page=99999999999999999999', '/r/my-room/playlists/netease/123'],
  ])('%s 规范化为 %s', (url, canonical) => {
    const route = parse(url)
    expect(route && formatRoute(route)).toBe(canonical)
  })

  it.each(['/r/', '/r/my-room/listen', '/r/my-room/queue/extra', '/settings/profile', '/settings/', '/settings/account/extra', '/admin/', '/admin/overview', '/r/my-room/playlists/bilibili/123', '/r/my-room/playlists/netease', '/s/', '/s/abc/def'])('%s 不对应任何页面', (url) => {
    expect(parse(url)).toBeNull()
  })

  it('解析设置和后台的分类', () => {
    expect(parseRoute({ pathname: '/settings/appearance', search: '', hash: '' })).toEqual({ name: 'settings', section: 'appearance', panel: null })
    expect(parseRoute({ pathname: '/settings', search: '', hash: '' })).toEqual({ name: 'settings', section: null, panel: null })
    expect(parseRoute({ pathname: '/settings/unknown', search: '', hash: '' })).toBeNull()
    expect(parseRoute({ pathname: '/admin/media', search: '?q=lemon&sort=size&page=3', hash: '' })).toEqual({ name: 'admin', section: 'media', panel: null, q: 'lemon', page: 3, sort: 'size' })
    expect(parseRoute({ pathname: '/admin/media', search: '?sort=bogus&page=0', hash: '' })).toEqual({ name: 'admin', section: 'media', panel: null, q: '', page: 1, sort: 'stored' })
  })

  it('媒体的搜索词截到接口允许的 100 个字符，按码点计；房间的搜索词在本地筛选，不截', () => {
    const long = `${'a'.repeat(99)}🎵🎵`
    expect(parse(`/admin/media?q=${encodeURIComponent(long)}`)).toMatchObject({ section: 'media', q: `${'a'.repeat(99)}🎵` })
    expect(parse(`/admin/media?q=${'b'.repeat(100)}`)).toMatchObject({ q: 'b'.repeat(100) })
    expect(parse(`/admin/rooms?q=${'c'.repeat(150)}`)).toMatchObject({ section: 'rooms', q: 'c'.repeat(150) })
  })

  it('写回地址时省略默认值', () => {
    expect(formatRoute({ name: 'admin', section: 'media', panel: null, q: '', page: 1, sort: 'stored' })).toBe('/admin/media')
    expect(formatRoute({ name: 'admin', section: 'rooms', panel: null, q: 'jazz', page: 2, sort: 'stored' })).toBe('/admin/rooms?q=jazz&page=2')
  })

  it('配对码从片段中读出，写回的地址不再带它', () => {
    const route = parse('/pair?panel=chat#ABCD2345')
    expect(route).toEqual({ name: 'pair', code: 'ABCD2345', panel: 'chat' })
    expect(route && formatRoute(route)).toBe('/pair?panel=chat')
    expect(parse('/pair')).toEqual({ name: 'pair', code: '', panel: null })
  })

  it('github 登录的结果从片段中读出，写回的地址不再带它', () => {
    const route = parse('/auth/github#code=abc')
    expect(route).toEqual({ name: 'github-login', fragment: 'code=abc', panel: null })
    expect(route && formatRoute(route)).toBe('/auth/github')
    expect(parse('/auth/github#error=denied')).toEqual({ name: 'github-login', fragment: 'error=denied', panel: null })
    expect(parse('/auth/github/x')).toBeNull()
  })
})

describe('isNewEntry', () => {
  it('换页面和进入另一个房间写入新记录', () => {
    expect(isNewEntry({ name: 'lobby', filter: '', panel: null }, { name: 'listen', ref: 'a', panel: null })).toBe(true)
    expect(isNewEntry({ name: 'listen', ref: 'a', panel: null }, { name: 'queue', ref: 'a', panel: null })).toBe(true)
    expect(isNewEntry({ name: 'listen', ref: 'a', panel: null }, { name: 'listen', ref: 'b', panel: null })).toBe(true)
  })

  it('开关侧栏和输入筛选词只替换当前地址', () => {
    expect(isNewEntry({ name: 'queue', ref: 'a', panel: null }, { name: 'queue', ref: 'a', panel: 'chat' })).toBe(false)
    expect(isNewEntry({ name: 'settings', section: null, panel: null }, { name: 'settings', section: null, panel: 'chat' })).toBe(false)
    expect(isNewEntry({ name: 'history', filter: '晴', panel: null }, { name: 'history', filter: '晴天', panel: null })).toBe(false)
  })

  it('换分类写入新记录，换搜索词、排序和页码只替换当前地址', () => {
    const admin = (section: AdminSection | null, q: string, page: number, sort: MediaSort): Route => ({ name: 'admin', section, panel: null, q, page, sort })
    expect(isNewEntry({ name: 'settings', section: null, panel: null }, { name: 'settings', section: 'account', panel: null })).toBe(true)
    expect(isNewEntry({ name: 'settings', section: 'account', panel: null }, { name: 'settings', section: 'privacy', panel: null })).toBe(true)
    expect(isNewEntry({ name: 'settings', section: 'privacy', panel: null }, { name: 'settings', section: null, panel: null })).toBe(true)
    expect(isNewEntry(admin('rooms', '', 1, 'stored'), admin('media', '', 1, 'stored'))).toBe(true)
    expect(isNewEntry(admin('media', '', 1, 'stored'), admin('media', 'lemon', 1, 'stored'))).toBe(false)
    expect(isNewEntry(admin('media', 'lemon', 1, 'stored'), admin('media', 'lemon', 2, 'size'))).toBe(false)
  })

  it('打开另一个自己的歌单写入新记录，同一个歌单只换侧栏时替换当前地址', () => {
    expect(isNewEntry({ name: 'my-playlist', id: 'a', panel: null }, { name: 'my-playlist', id: 'b', panel: null })).toBe(true)
    expect(isNewEntry({ name: 'my-playlist', id: 'a', panel: null }, { name: 'my-playlist', id: 'a', panel: 'queue' })).toBe(false)
  })

  it('提交新的关键词写入新记录，换来源、类别、页码和推荐只替换当前地址', () => {
    const search = (query: SearchQuery): Route => ({ name: 'search', ref: 'a', panel: null, query })
    const sunny = search({ view: 'all', keyword: '晴天', source: 'qq', page: 1 })
    expect(isNewEntry(sunny, search({ view: 'all', keyword: '七里香', source: 'qq', page: 1 }))).toBe(true)
    expect(isNewEntry(sunny, search({ view: 'all', keyword: '', source: 'qq', page: 1 }))).toBe(true)
    expect(isNewEntry(sunny, search({ view: 'tracks', keyword: '晴天', source: 'netease', page: 2 }))).toBe(false)
    expect(isNewEntry(sunny, search({ view: 'recommend' }))).toBe(false)
    expect(isNewEntry(search({ view: 'recommend' }), search({ view: 'tracks', keyword: '', source: 'qq', page: 1 }))).toBe(false)
    expect(isNewEntry(search({ view: 'recommend' }), sunny)).toBe(true)
  })

  it('打开另一个歌单写入新记录，翻页只替换当前地址', () => {
    const playlist = (id: string, page: number): Route => ({ name: 'playlist', ref: 'a', panel: null, source: 'netease', id, page })
    expect(isNewEntry(playlist('1', 1), playlist('2', 1))).toBe(true)
    expect(isNewEntry(playlist('1', 1), playlist('1', 2))).toBe(false)
  })

  it('打开另一个分享写入新记录，同一个分享只换侧栏时替换当前地址', () => {
    expect(isNewEntry({ name: 'shared-playlist', shareId: 'a', panel: null }, { name: 'shared-playlist', shareId: 'b', panel: null })).toBe(true)
    expect(isNewEntry({ name: 'shared-playlist', shareId: 'a', panel: null }, { name: 'shared-playlist', shareId: 'a', panel: 'queue' })).toBe(false)
  })
})

describe('upStep', () => {
  const list: Route = { name: 'settings', section: null, panel: null }

  it('上一条记录就是分类列表时后退，不新增记录', () => {
    expect(upStep(list, '/settings', true)).toBe('back')
    expect(upStep({ name: 'admin', section: null, panel: null, q: '', page: 1, sort: 'stored' }, '/admin', true)).toBe('back')
  })

  it('从别的页面直接打开分类、或者没有可以后退的记录时，把当前记录换成分类列表', () => {
    expect(upStep(list, '/playlists', true)).toBe('replace')
    expect(upStep(list, '/settings/account', true)).toBe('replace')
    expect(upStep(list, undefined, true)).toBe('replace')
    expect(upStep(list, '/settings', false)).toBe('replace')
  })
})

describe('leaveStep', () => {
  const leaving = new Set(['my-room', 'a1b2c3'])

  it('不在这个房间的页面上时留在原处', () => {
    expect(leaveStep({ name: 'admin', section: null, panel: null, q: '', page: 1, sort: 'stored' }, leaving, true)).toBe('stay')
    expect(leaveStep({ name: 'listen', ref: 'other-room', panel: null }, leaving, true)).toBe('stay')
  })

  it('在这个房间的页面上一直后退，直接打开的房间换成发现房间', () => {
    expect(leaveStep({ name: 'queue', ref: 'a1b2c3', panel: 'chat' }, leaving, true)).toBe('back')
    expect(leaveStep({ name: 'listen', ref: 'my-room', panel: null }, leaving, false)).toBe('lobby')
  })
})
