import type { MyPlaylist, MyPlaylistItem } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeStorage } from '@/core/api/fake-storage'

const playlist: MyPlaylist = { id: 'p1', name: '通勤', favorites: false, trackCount: 3, createdAtMs: 1, updatedAtMs: 1, coverUrl: null, shareId: null }

function song(id: string): MyPlaylistItem {
  return { track: { source: 'netease', id }, meta: { title: id, artist: '', album: '', coverUrl: '', pageUrl: '', durationMs: 0 } }
}

const [a, b, c] = [song('a'), song('b'), song('c')]
const key = (item: MyPlaylistItem) => `${item.track.source}:${item.track.id}`

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status })
}

// 服务端的曲目。读取在请求到达时取值，由测试决定何时返回。
let server: MyPlaylistItem[]
let reads: ((response?: Response) => void)[]
let writes: { route: string, body: unknown, settle: (ok: boolean) => void }[]

const flush = () => new Promise(resolve => setTimeout(resolve, 0))

async function answerRead(response?: Response): Promise<void> {
  await flush()
  reads.shift()!(response)
  await flush()
}

async function answerWrite(ok: boolean): Promise<void> {
  await flush()
  writes.shift()!.settle(ok)
  await flush()
}

async function load() {
  const { PlaylistTracksStore } = await import('./playlist-tracks-store')
  const { notificationStore } = await import('@/shared/lib/notification-store')
  const store = new PlaylistTracksStore('p1')
  const ids = () => store.state.items?.map(item => item.track.id).join('') ?? null
  const selected = () => [...store.state.selection.keys].sort().join(',')
  return { store, ids, selected, notificationStore }
}

async function loaded() {
  const page = await load()
  void page.store.load()
  await answerRead()
  return page
}

beforeEach(() => {
  vi.resetModules()
  server = [a, b, c]
  reads = []
  writes = []
  const local = new FakeStorage()
  local.setItem('alisten.member-token', 'good')
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('fetch', (path: string, init: RequestInit) => {
    const route = `${init.method} ${path}`
    if (route === 'GET /api/me/playlists')
      return Promise.resolve(json(200, { playlists: [playlist] }))
    if (route === 'GET /api/me/playlists/p1/items') {
      const items = server
      return new Promise<Response>(resolve => reads.push(response => resolve(response ?? json(200, { items }))))
    }
    return new Promise<Response>((resolve) => {
      writes.push({
        route,
        body: JSON.parse(String(init.body)),
        settle: ok => resolve(ok ? new Response(null, { status: 204 }) : json(409, { error: { code: 'CONFLICT', message: '歌单已经变了，刷新之后再试' } })),
      })
    })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('读取', () => {
  it('读到之前没有曲目，读到之后按服务端的顺序', async () => {
    const { store, ids } = await load()
    void store.load()
    expect(ids()).toBeNull()
    await answerRead()
    expect(ids()).toBe('abc')
  })

  it('读取失败时记下原因，重试成功之后清掉', async () => {
    const { store, ids } = await load()
    void store.load()
    await answerRead(json(503, { error: { code: 'UNAVAILABLE', message: '服务暂时不可用，请稍后再试。' } }))
    expect(store.state).toMatchObject({ items: null, failed: '服务暂时不可用，请稍后再试。' })

    store.retry()
    expect(store.state.failed).toBeNull()
    await answerRead()
    expect(store.state.failed).toBeNull()
    expect(ids()).toBe('abc')
  })

  it('先发起的读取晚返回时不用它的结果', async () => {
    const { store, ids } = await load()
    void store.load()
    void store.load()
    await flush()
    const [first, second] = reads.splice(0)
    second!(json(200, { items: [c, b, a] }))
    await flush()
    expect(ids()).toBe('cba')
    first!(json(200, { items: [a, b, c] }))
    await flush()
    expect(ids()).toBe('cba')
  })

  it('重新读取之后，已经不在歌单里的歌从选择里去掉', async () => {
    const { store, selected } = await loaded()
    store.select({ keys: new Set([key(a), key(b)]), anchor: key(a) })
    server = [b, c]
    void store.load()
    await answerRead()
    expect(selected()).toBe(key(b))
    expect(store.state.selection.anchor).toBeNull()
  })
})

describe('移动', () => {
  it('先在本地换好顺序再请求，成功之后以服务端的为准', async () => {
    const { store, ids } = await loaded()
    store.move(playlist, { order: [key(c), key(a), key(b)], moved: [key(c)], before: key(a) })
    expect(ids()).toBe('cab')
    await flush()
    expect(writes[0]).toMatchObject({ route: 'POST /api/me/playlists/p1/moves', body: { tracks: [c.track], before: a.track } })

    server = [c, a, b]
    await answerWrite(true)
    while (reads.length > 0)
      await answerRead()
    expect(ids()).toBe('cab')
  })

  it('失败时回到移动之前的顺序并提示', async () => {
    const { store, ids, notificationStore } = await loaded()
    store.move(playlist, { order: [key(b), key(c), key(a)], moved: [key(a)], before: undefined })
    expect(ids()).toBe('bca')
    await answerWrite(false)
    expect(ids()).toBe('abc')
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual(['歌单已经变了，刷新之后再试'])
  })

  it('移动还没有结果时返回的读取不用，移动结束之后再读一次', async () => {
    const { store, ids } = await loaded()
    void store.load()
    store.move(playlist, { order: [key(b), key(a), key(c)], moved: [key(b)], before: key(a) })
    // 这次读取在移动之前到达服务端，带回的是旧的顺序。
    await answerRead()
    expect(ids()).toBe('bac')

    server = [b, a, c]
    await answerWrite(true)
    expect(reads.length).toBeGreaterThan(0)
    while (reads.length > 0)
      await answerRead()
    expect(ids()).toBe('bac')
  })
})

describe('移出', () => {
  it('先在本地移出并从选择里去掉，成功之后以服务端的为准', async () => {
    const { store, ids, selected } = await loaded()
    store.select({ keys: new Set([key(a), key(b)]), anchor: key(b) })
    const removing = store.remove(playlist, [b.track])
    expect(ids()).toBe('ac')
    expect(selected()).toBe(key(a))
    await flush()
    expect(writes[0]).toMatchObject({ route: 'POST /api/me/playlists/p1/removals', body: { tracks: [b.track] } })

    server = [a, c]
    await answerWrite(true)
    await removing
    while (reads.length > 0)
      await answerRead()
    expect(ids()).toBe('ac')
  })

  it('失败时曲目和选择都回到移出之前', async () => {
    const { store, ids, selected } = await loaded()
    store.select({ keys: new Set([key(a), key(b)]), anchor: key(b) })
    const removing = store.remove(playlist, [b.track])
    await answerWrite(false)
    await removing
    expect(ids()).toBe('abc')
    expect(selected()).toBe([key(a), key(b)].join(','))
    expect(store.state.selection.anchor).toBe(key(b))
  })

  it('还没读到曲目时什么也不做', async () => {
    const { store } = await load()
    await store.remove(playlist, [a.track])
    store.move(playlist, { order: [key(a)], moved: [key(a)], before: undefined })
    await flush()
    expect(writes).toEqual([])
  })
})
