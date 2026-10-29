import type { Route } from './route'
import type { SearchQuery } from '@/features/search'
import { describe, expect, it } from 'vitest'
import { formatRoute, isNewEntry, leaveStep, parseRoute } from './route'

function parse(url: string): Route | null {
  return parseRoute(new URL(url, 'https://alisten.test'))
}

describe('parseRoute', () => {
  it.each<[string, Route]>([
    ['/', { name: 'lobby', filter: '', panel: null }],
    ['/?q=+%E5%A4%9C&panel=chat', { name: 'lobby', filter: ' 夜', panel: 'chat' }],
    ['/admin', { name: 'admin', panel: null }],
    ['/settings?panel=queue', { name: 'settings', panel: 'queue' }],
    ['/history', { name: 'history', filter: '', panel: null }],
    ['/history?q=%E6%99%B4&panel=history', { name: 'history', filter: '晴', panel: 'history' }],
    ['/r/my-room', { name: 'listen', ref: 'my-room', panel: null }],
    ['/r/my-room?panel=history', { name: 'listen', ref: 'my-room', panel: 'history' }],
    ['/r/my-room/queue?panel=queue', { name: 'queue', ref: 'my-room', panel: 'queue' }],
    ['/r/my-room/chat', { name: 'chat', ref: 'my-room', panel: null }],
    ['/r/my-room/search', { name: 'search', ref: 'my-room', panel: null, query: { view: 'all', keyword: '', source: 'qq', page: 1 } }],
    ['/r/my-room/search?q=%E6%99%B4%E5%A4%A9&source=netease&view=playlists&page=2&panel=chat', { name: 'search', ref: 'my-room', panel: 'chat', query: { view: 'playlists', keyword: '晴天', source: 'netease', page: 2 } }],
    ['/r/my-room/search?source=netease&view=tracks', { name: 'search', ref: 'my-room', panel: null, query: { view: 'tracks', keyword: '', source: 'netease', page: 1 } }],
    ['/r/my-room/search?view=recommend', { name: 'search', ref: 'my-room', panel: null, query: { view: 'recommend' } }],
    ['/r/my-room/playlists/netease/123?page=3&panel=queue', { name: 'playlist', ref: 'my-room', panel: 'queue', source: 'netease', id: '123', page: 3 }],
  ])('%s', (url, route) => {
    expect(parse(url)).toEqual(route)
    expect(formatRoute(route)).toBe(url)
  })

  it.each([
    ['/admin?from=menu', '/admin'],
    ['/settings?panel=lyrics', '/settings'],
    ['/?q=', '/'],
    ['/r/my-room?panel=lyrics', '/r/my-room'],
    ['/r/my-room/search?q=%20a%20', '/r/my-room/search?q=a&source=qq'],
    ['/r/my-room/search?q=a&source=kuwo&view=lyrics&page=0', '/r/my-room/search?q=a&source=qq'],
    ['/r/my-room/search?q=a&source=qq&view=playlists', '/r/my-room/search?q=a&source=qq'],
    ['/r/my-room/search?q=a&page=2', '/r/my-room/search?q=a&source=qq'],
    ['/r/my-room/search?view=tracks&page=2', '/r/my-room/search?view=tracks'],
    ['/r/my-room/search?view=recommend&q=a&source=netease&page=2', '/r/my-room/search?view=recommend'],
    ['/r/my-room/playlists/netease/123?page=1.5', '/r/my-room/playlists/netease/123'],
  ])('%s 规范化为 %s', (url, canonical) => {
    const route = parse(url)
    expect(route && formatRoute(route)).toBe(canonical)
  })

  it.each(['/r/', '/r/my-room/listen', '/r/my-room/queue/extra', '/settings/profile', '/r/my-room/playlists/qq/123', '/r/my-room/playlists/netease'])('%s 不对应任何页面', (url) => {
    expect(parse(url)).toBeNull()
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
    expect(isNewEntry({ name: 'settings', panel: null }, { name: 'settings', panel: 'chat' })).toBe(false)
    expect(isNewEntry({ name: 'history', filter: '晴', panel: null }, { name: 'history', filter: '晴天', panel: null })).toBe(false)
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
})

describe('leaveStep', () => {
  const leaving = new Set(['my-room', 'a1b2c3'])

  it('不在这个房间的页面上时留在原处', () => {
    expect(leaveStep({ name: 'admin', panel: null }, leaving, true)).toBe('stay')
    expect(leaveStep({ name: 'listen', ref: 'other-room', panel: null }, leaving, true)).toBe('stay')
  })

  it('在这个房间的页面上一直后退，直接打开的房间换成发现房间', () => {
    expect(leaveStep({ name: 'queue', ref: 'a1b2c3', panel: 'chat' }, leaving, true)).toBe('back')
    expect(leaveStep({ name: 'listen', ref: 'my-room', panel: null }, leaving, false)).toBe('lobby')
  })
})
