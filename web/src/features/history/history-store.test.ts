import type { MyListen, NowPlaying, Track } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

class FakeStorage {
  readonly #items = new Map<string, string>()

  get length(): number {
    return this.#items.size
  }

  key(index: number): string | null {
    return [...this.#items.keys()][index] ?? null
  }

  getItem(key: string): string | null {
    return this.#items.get(key) ?? null
  }

  setItem(key: string, value: string): void {
    this.#items.set(key, value)
  }

  removeItem(key: string): void {
    this.#items.delete(key)
  }
}

function listen(id: string, heardAtMs: number): MyListen {
  return {
    track: { source: 'netease', id },
    meta: { title: id, artist: '', album: '', coverUrl: '', pageUrl: '', durationMs: 1000 },
    heardAtMs,
  }
}

function nowPlaying(entryId: string, startedAtMs: number): NowPlaying {
  return { entry: { id: entryId }, startedAtMs } as NowPlaying
}

let requests: string[]
let server: MyListen[]
// 读取的结果在请求到达服务端时确定，holdReads 时推迟返回，模拟带着旧状态返回的读取。
let holdReads: boolean
let release: (() => void)[]
let failReads: boolean
// 服务端拒绝这个成员 token，客户端随即换成新的游客 renewed。
let rejectedToken: string | null
let room: EventTarget & { state: { room: { nowPlaying: NowPlaying | null } | null } }
const renewed = { id: 'm2', name: '游客-9f8e', avatarHash: '' }

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status })
}

beforeEach(() => {
  vi.resetModules()
  requests = []
  server = [listen('2', 2), listen('1', 1)]
  holdReads = false
  release = []
  failReads = false
  rejectedToken = null
  room = Object.assign(new EventTarget(), { state: { room: null } })
  vi.doMock('@/core/room', () => ({ roomStore: room }))
  vi.stubGlobal('window', new EventTarget())
  const local = new FakeStorage()
  local.setItem('alisten.member-token', 'token')
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const request = `${init.method} ${path}`
    requests.push(request)
    if (new Headers(init.headers).get('Authorization') === `Bearer ${rejectedToken}`)
      return json(401, { error: { code: 'UNAUTHORIZED', message: '身份已经失效' } })
    if (request === 'POST /api/members')
      return json(201, { member: renewed, memberToken: 'token-2' })
    if (request === 'GET /api/me')
      return json(200, { member: renewed, recordListens: true })
    if (request === 'GET /api/me/listens') {
      const items = [...server]
      if (holdReads)
        await new Promise<void>(resolve => release.push(resolve))
      if (failReads)
        return json(503, { error: { code: 'UNAVAILABLE', message: '暂时不可用' } })
      return json(200, { items })
    }
    if (request === 'POST /api/me/listens/removals') {
      const { track } = JSON.parse(String(init.body)) as { track: Track }
      server = server.filter(l => l.track.id !== track.id)
      return new Response(null, { status: 204 })
    }
    if (request === 'DELETE /api/me/listens') {
      server = []
      return new Response(null, { status: 204 })
    }
    throw new Error(`没有预期的请求 ${request}`)
  })
})

afterEach(() => {
  vi.doUnmock('@/core/room')
  vi.unstubAllGlobals()
})

function flush() {
  return new Promise(resolve => setTimeout(resolve, 0))
}

function play(next: NowPlaying | null) {
  room.state = { room: next && { nowPlaying: next } }
  room.dispatchEvent(new Event('change'))
}

async function started() {
  const { historyStore } = await import('./history-store')
  historyStore.start()
  return historyStore
}

const reads = () => requests.filter(r => r === 'GET /api/me/listens').length

describe('historyStore 的播放历史', () => {
  it('每次打开页面都从服务端读取', async () => {
    const historyStore = await started()
    expect(historyStore.state.listens).toBeNull()
    const close = historyStore.open()
    await flush()
    expect(historyStore.state.listens).toEqual(server)
    close()
    historyStore.open()
    await flush()
    expect(reads()).toBe(2)
  })

  it('页面开着时，正在播放的歌一变就重新读取；同一首歌的其他房间变化不读', async () => {
    const historyStore = await started()
    historyStore.open()
    await flush()
    play(nowPlaying('1', 1000))
    await flush()
    expect(reads()).toBe(2)
    play(nowPlaying('1', 1000))
    play(null)
    await flush()
    expect(reads()).toBe(2)
    server = [listen('3', 3), ...server]
    play(nowPlaying('2', 2000))
    await flush()
    expect(reads()).toBe(3)
    expect(historyStore.state.listens?.[0]).toEqual(listen('3', 3))
  })

  it('页面都关了之后换歌不读取', async () => {
    const historyStore = await started()
    const closeFull = historyStore.open()
    const closePanel = historyStore.open()
    await flush()
    closeFull()
    play(nowPlaying('1', 1000))
    await flush()
    expect(reads()).toBe(3)
    closePanel()
    closePanel()
    play(nowPlaying('2', 2000))
    await flush()
    expect(reads()).toBe(3)
  })

  it('只有最后发起的读取能写入', async () => {
    const historyStore = await started()
    holdReads = true
    historyStore.open()
    await flush()
    server = [listen('3', 3), ...server]
    play(nowPlaying('1', 1000))
    await flush()
    release.pop()?.()
    await flush()
    release.pop()?.()
    await flush()
    expect(historyStore.state.listens?.map(l => l.track.id)).toEqual(['3', '2', '1'])
  })

  it('删除之后，删除之前发起的读取作废，再读一次', async () => {
    const historyStore = await started()
    historyStore.open()
    await flush()
    holdReads = true
    play(nowPlaying('1', 1000))
    await flush()
    holdReads = false
    expect(await historyStore.remove(listen('2', 2))).toBe(true)
    expect(historyStore.state.listens).toEqual([listen('1', 1)])
    await flush()
    release.shift()?.()
    await flush()
    expect(historyStore.state.listens).toEqual([listen('1', 1)])
    expect(requests).toEqual(['GET /api/me/listens', 'GET /api/me/listens', 'POST /api/me/listens/removals', 'GET /api/me/listens'])
  })

  it('清空', async () => {
    const historyStore = await started()
    historyStore.open()
    await flush()
    expect(await historyStore.clear()).toBe(true)
    expect(historyStore.state.listens).toEqual([])
    expect(requests).toEqual(['GET /api/me/listens', 'DELETE /api/me/listens'])
  })

  it('读取失败时记下原因，已经读到的历史保留', async () => {
    const historyStore = await started()
    historyStore.open()
    await flush()
    failReads = true
    play(nowPlaying('1', 1000))
    await flush()
    expect(historyStore.state.error).toBe('暂时不可用')
    expect(historyStore.state.listens).toEqual(server)
    failReads = false
    play(nowPlaying('2', 2000))
    await flush()
    expect(historyStore.state.error).toBeNull()
  })

  it('身份换成新的游客时清掉旧身份的历史，进行中的读取作废；页面开着时换好之后重新读取', async () => {
    const historyStore = await started()
    const { identityEvents } = await import('@/core/api')
    historyStore.open()
    await flush()
    holdReads = true
    play(nowPlaying('1', 1000))
    await flush()
    identityEvents.emit({ kind: 'rejected' })
    expect(historyStore.state.listens).toEqual([])
    holdReads = false
    server = [listen('9', 9)]
    identityEvents.emit({ kind: 'renewed', member: renewed })
    await flush()
    release.shift()?.()
    await flush()
    expect(historyStore.state.listens).toEqual([listen('9', 9)])
    expect(reads()).toBe(3)
  })

  it('页面都关了时换身份只清掉旧身份的历史，不读取', async () => {
    const historyStore = await started()
    const { identityEvents } = await import('@/core/api')
    historyStore.open()()
    await flush()
    identityEvents.emit({ kind: 'rejected' })
    identityEvents.emit({ kind: 'renewed', member: renewed })
    await flush()
    expect(historyStore.state.listens).toEqual([])
    expect(reads()).toBe(1)
  })

  it('读取本身被拒绝时，换成新的游客之后读取它的历史', async () => {
    const historyStore = await started()
    rejectedToken = 'token'
    server = [listen('9', 9)]
    historyStore.open()
    await flush()
    expect(historyStore.state).toMatchObject({ listens: [listen('9', 9)], error: null })
    expect(requests).toEqual(['GET /api/me/listens', 'POST /api/members', 'GET /api/me', 'GET /api/me/listens'])
  })
})
