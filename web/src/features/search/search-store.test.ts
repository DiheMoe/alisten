import type { TrackSummary } from '@/core/realtime'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { cachedResult, nextLoad, rememberResult } from './search-store'

describe('nextLoad', () => {
  it('条件没变时沿用当前结果，强制刷新时绕过缓存重新请求', () => {
    expect(nextLoad('a', 'a', true, true, false)).toBeNull()
    expect(nextLoad('a', 'a', true, true, true)).toEqual({ shown: 'a', loading: true, from: 'request' })
  })

  it('没有关键词时显示最近搜索，不发出请求', () => {
    expect(nextLoad(null, 'a', false, true, false)).toEqual({ shown: null, loading: false, from: 'none' })
  })

  it('回到已经显示过的条件时直接用缓存，不必连着房间', () => {
    expect(nextLoad('a', 'b', true, false, false)).toEqual({ shown: 'a', loading: false, from: 'cache' })
  })

  it('没连上房间时推迟请求并让当前结果作废，连上之后回到原来的条件也会重新请求', () => {
    expect(nextLoad('a', 'b', false, false, false)).toEqual({ shown: null, loading: true, from: 'none' })
    expect(nextLoad('b', null, false, false, false)).toEqual({ shown: null, loading: true, from: 'none' })
    expect(nextLoad('b', null, false, true, false)).toEqual({ shown: 'b', loading: true, from: 'request' })
  })
})

describe('搜索结果的缓存', () => {
  const minute = 60 * 1000

  it('取回超过 10 分钟的结果失效', () => {
    const cache = rememberResult(new Map(), 'a', { value: 1, at: 0 }, 0)
    expect(cachedResult(cache, 'a', 10 * minute - 1)?.value).toBe(1)
    expect(cachedResult(cache, 'a', 10 * minute)).toBeNull()
    expect(cachedResult(cache, 'b', 0)).toBeNull()
  })

  it('最多保留 10 份，丢掉最久没显示的；再次显示的移到最近', () => {
    let cache = new Map<string, { value: number, at: number }>()
    for (let i = 0; i < 10; i++)
      cache = rememberResult(cache, `k${i}`, { value: i, at: i }, i)
    cache = rememberResult(cache, 'k0', { value: 0, at: 0 }, 10)
    cache = rememberResult(cache, 'k10', { value: 10, at: 11 }, 11)
    expect([...cache.keys()]).toEqual(['k2', 'k3', 'k4', 'k5', 'k6', 'k7', 'k8', 'k9', 'k0', 'k10'])
  })

  it('记下新结果时去掉已经失效的', () => {
    const cache = rememberResult(new Map([['old', { value: 1, at: 0 }]]), 'new', { value: 2, at: 10 * minute }, 10 * minute)
    expect([...cache.keys()]).toEqual(['new'])
  })
})

interface Call {
  name: string
  params: unknown
  resolve: (value: unknown) => void
  reject: (reason: unknown) => void
}

function song(id: string): TrackSummary {
  return { track: { source: 'qq', id } } as unknown as TrackSummary
}

function found(...ids: string[]) {
  return { items: ids.map(song), total: ids.length }
}

let calls: Call[]
let room: ReturnType<typeof fakeRoom>

function fakeRoom() {
  const request = (name: string) => (params: unknown = null) => new Promise((resolve, reject) => {
    calls.push({ name, params, resolve, reject })
  })
  return Object.assign(new EventTarget(), {
    state: { roomId: 'r1' as string | null, status: 'open' },
    requests: {
      searchTracks: request('searchTracks'),
      searchPlaylists: request('searchPlaylists'),
      recommendList: request('recommendList'),
      playlistTracks: request('playlistTracks'),
    },
  })
}

function setRoom(patch: Partial<typeof room.state>): void {
  room.state = { ...room.state, ...patch }
  room.dispatchEvent(new Event('change'))
}

function call(index: number): Call {
  const found = calls[index]
  if (!found)
    throw new Error(`没有第 ${index} 个请求`)
  return found
}

async function settled(): Promise<void> {
  await new Promise(resolve => setTimeout(resolve))
}

async function load() {
  const search = await import('./search-store')
  const { notificationStore } = await import('@/shared/ui')
  search.searchStore.start()
  const ids = () => search.searchStore.state.tracks.map(({ track }) => track.id)
  const tracks = (keyword: string, source: 'qq' | 'netease' = 'qq') => search.keywordSearch(keyword, source, 'tracks')
  return { ...search, notificationStore, ids, tracks }
}

describe('搜索请求', () => {
  beforeEach(() => {
    vi.resetModules()
    calls = []
    room = fakeRoom()
    vi.doMock('@/core/room', () => ({ roomStore: room }))
  })

  it('换了条件之后，先发出的请求晚到也不显示', async () => {
    const { searchStore, ids, tracks } = await load()
    searchStore.show(tracks('a'))
    searchStore.show(tracks('b'))
    call(1).resolve(found('b1'))
    await settled()
    call(0).resolve(found('a1'))
    await settled()
    expect(ids()).toEqual(['b1'])
    expect(searchStore.state.loading).toBe(false)
  })

  it('换了房间之后，上一个房间的响应不显示，连上之后重新请求', async () => {
    const { searchStore, ids, tracks } = await load()
    searchStore.show(tracks('a'))
    setRoom({ roomId: 'r2', status: 'connecting' })
    call(0).resolve(found('old'))
    await settled()
    expect(ids()).toEqual([])
    expect(calls).toHaveLength(1)

    setRoom({ status: 'open' })
    expect(calls).toHaveLength(2)
    call(1).resolve(found('new'))
    await settled()
    expect(ids()).toEqual(['new'])
    expect(searchStore.state.loading).toBe(false)
  })

  it('回到显示过的条件时用缓存，不发请求', async () => {
    const { searchStore, ids, tracks } = await load()
    searchStore.show(tracks('a'))
    call(0).resolve(found('a1'))
    await settled()
    searchStore.show(tracks('b'))
    call(1).resolve(found('b1'))
    await settled()

    searchStore.show(tracks('a'))
    expect(calls).toHaveLength(2)
    expect(ids()).toEqual(['a1'])
    expect(searchStore.state.loading).toBe(false)
  })

  it('用缓存之后，之前发出的请求晚到也不显示', async () => {
    const { searchStore, ids, tracks } = await load()
    searchStore.show(tracks('a'))
    call(0).resolve(found('a1'))
    await settled()
    searchStore.show(tracks('b'))
    searchStore.show(tracks('a'))
    call(1).resolve(found('b1'))
    await settled()
    expect(ids()).toEqual(['a1'])
  })

  it('换了房间之后缓存作废', async () => {
    const { searchStore, tracks } = await load()
    searchStore.show(tracks('a'))
    call(0).resolve(found('a1'))
    await settled()
    setRoom({ roomId: 'r2' })
    expect(calls).toHaveLength(2)
  })

  it('同样的条件不重复请求', async () => {
    const { searchStore, tracks } = await load()
    searchStore.show(tracks('a'))
    searchStore.show(tracks('a'))
    expect(calls).toHaveLength(1)
  })

  it('没连上房间时推迟请求，连上之后补发', async () => {
    room.state.status = 'connecting'
    const { searchStore, tracks } = await load()
    searchStore.show(tracks('a'))
    expect(calls).toHaveLength(0)
    expect(searchStore.state.loading).toBe(true)

    setRoom({ status: 'open' })
    expect(calls).toHaveLength(1)
  })

  it('页面没有显示时，连上房间也不发请求', async () => {
    room.state.status = 'connecting'
    const { searchStore, tracks } = await load()
    searchStore.show(tracks('a'))
    searchStore.hide()
    setRoom({ status: 'open' })
    expect(calls).toHaveLength(0)
  })

  it('失败时提示一次、不缓存，回到这个条件时重新请求', async () => {
    const { searchStore, notificationStore, tracks } = await load()
    searchStore.show(tracks('a'))
    call(0).reject(new Error('出错了'))
    await settled()
    expect(searchStore.state).toMatchObject({ loading: false, failed: true })
    expect(notificationStore.state.notifications).toHaveLength(1)

    searchStore.show(tracks('b'))
    call(1).resolve(found('b1'))
    await settled()
    expect(searchStore.state.failed).toBe(false)
    searchStore.show(tracks('a'))
    expect(calls).toHaveLength(3)
  })

  it('重试时绕过缓存', async () => {
    const { searchStore, ids, tracks } = await load()
    searchStore.show(tracks('a'))
    call(0).resolve(found('a1'))
    await settled()
    searchStore.retry()
    expect(calls).toHaveLength(2)
    call(1).resolve(found('a2'))
    await settled()
    expect(ids()).toEqual(['a2'])
  })

  it('同时搜歌曲和歌单时，一半失败也显示另一半', async () => {
    const { searchStore, notificationStore, ids, keywordSearch } = await load()
    searchStore.show(keywordSearch('a', 'netease', 'all'))
    expect(calls.map(({ name }) => name)).toEqual(['searchTracks', 'searchPlaylists'])
    call(0).resolve(found('a1'))
    call(1).reject(new Error('出错了'))
    await settled()
    expect(ids()).toEqual(['a1'])
    expect(searchStore.state.failed).toBe(true)
    expect(notificationStore.state.notifications).toHaveLength(1)
  })

  it('两个请求因为同样的原因失败时只提示一次', async () => {
    const { searchStore, notificationStore, keywordSearch } = await load()
    searchStore.show(keywordSearch('a', 'netease', 'all'))
    call(0).reject(new Error('出错了'))
    call(1).reject(new Error('出错了'))
    await settled()
    expect(notificationStore.state.notifications).toHaveLength(1)
  })

  it('只有网易云搜歌单：其他来源的全部类别只搜歌曲', async () => {
    const { searchStore, keywordSearch } = await load()
    searchStore.show(keywordSearch('a', 'qq', 'all'))
    expect(calls.map(({ name }) => name)).toEqual(['searchTracks'])
  })

  it('关键词太长时提示长度上限', async () => {
    const { RequestError } = await import('@/core/realtime')
    const { searchStore, notificationStore, tracks } = await load()
    searchStore.show(tracks('a'))
    call(0).reject(new RequestError('参数无效', 'INVALID_ARGUMENT'))
    await settled()
    expect(notificationStore.state.notifications[0]?.message).toBe('关键词太长了，最多 500 个字符。')
  })

  it('没有关键词时不请求，显示最近搜索', async () => {
    const { searchStore, tracks } = await load()
    searchStore.show(tracks(''))
    expect(calls).toHaveLength(0)
    expect(searchStore.state.loading).toBe(false)
  })

  it('推荐属于当前房间：换了房间就清空，连上之后重新请求', async () => {
    const { searchStore } = await load()
    searchStore.show({ view: 'recommend' })
    call(0).resolve({ items: [song('r1')] })
    await settled()
    expect(searchStore.state.recommendations).toHaveLength(1)

    setRoom({ roomId: 'r2', status: 'connecting' })
    expect(searchStore.state.recommendations).toEqual([])

    setRoom({ status: 'open' })
    expect(calls).toHaveLength(2)
  })

  it('换了房间时正在进行的推荐不再等待，不一直显示加载中', async () => {
    const { searchStore } = await load()
    searchStore.show({ view: 'recommend' })
    expect(searchStore.state.loading).toBe(true)

    setRoom({ roomId: 'r2', status: 'connecting' })
    expect(searchStore.state.loading).toBe(false)
  })

  it('打开歌单时显示歌单内的歌曲，同一个歌单不重复请求', async () => {
    const { searchStore, ids } = await load()
    searchStore.showPlaylist('netease', 'p1')
    searchStore.showPlaylist('netease', 'p1')
    expect(calls).toHaveLength(1)
    expect(call(0)).toMatchObject({ name: 'playlistTracks', params: { source: 'netease', playlistId: 'p1' } })
    call(0).resolve(found('p1-1'))
    await settled()
    expect(ids()).toEqual(['p1-1'])
  })

  it('条件没变时不动正在输入的文字，条件变了就跟着地址', async () => {
    const { searchStore, tracks } = await load()
    searchStore.show(tracks('a'))
    searchStore.setText('ab')
    searchStore.show(tracks('a'))
    expect(searchStore.state.text).toBe('ab')

    searchStore.show(tracks('c'))
    expect(searchStore.state.text).toBe('c')
    searchStore.show({ view: 'recommend' })
    expect(searchStore.state.text).toBe('')
  })
})
