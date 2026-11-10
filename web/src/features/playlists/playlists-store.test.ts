import type { Track } from '@/core/realtime'
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

const favorites = { id: 'p1', name: '我喜欢', favorites: true, trackCount: 1, createdAtMs: 1, updatedAtMs: 1, coverUrl: null }
const sunny = { source: 'netease', id: '186016' } as const
const rain = { source: 'netease', id: '185811' } as const
const commute = { id: 'p2', name: '通勤', favorites: false, trackCount: 0, createdAtMs: 1, updatedAtMs: 1, coverUrl: null }
const meta = { title: '晴天', artist: '周杰伦', album: '叶惠美', coverUrl: '', pageUrl: '', durationMs: 0 }
const notReady = '这首歌还在准备，准备好之后再保存'
const full = '一个歌单最多 1000 首歌'
const changed = '歌单已经变了，刷新之后再试'
const imported = { id: 'p3', name: '导入的歌单', favorites: false, trackCount: 2, createdAtMs: 2, updatedAtMs: 2, coverUrl: null }
const fileTrack = { track: sunny, title: '晴天', durationMs: 269000 }

let requests: string[]
let release: (() => void)[]
let failRemove: boolean
let failAdd: boolean
let failCopy: boolean
let failMove: boolean
let importFailure: { status: number, code: string, message: string } | null
let bodies: unknown[]
let holdReads: boolean
let failReads: boolean
let serverPlaylists: typeof favorites[]
let server: Map<string, Map<string, Track>>

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status })
}

function key(track: Track): string {
  return `${track.source}:${track.id}`
}

function items(playlist: string): Map<string, Track> {
  let tracks = server.get(playlist)
  if (!tracks) {
    tracks = new Map()
    server.set(playlist, tracks)
  }
  return tracks
}

// 读取的结果在请求到达服务端时确定，holdReads 时推迟返回，模拟带着旧状态返回的读取。
async function read(body: unknown): Promise<Response> {
  if (holdReads)
    await new Promise<void>(resolve => release.push(resolve))
  if (failReads)
    return json(503, { error: { code: 'UNAVAILABLE', message: '暂时不可用' } })
  return json(200, body)
}

beforeEach(() => {
  vi.resetModules()
  requests = []
  release = []
  failRemove = false
  failAdd = false
  failCopy = false
  failMove = false
  importFailure = null
  bodies = []
  holdReads = false
  failReads = false
  serverPlaylists = [favorites]
  server = new Map([['p1', new Map([[key(sunny), sunny]])]])
  vi.stubGlobal('window', new EventTarget())
  const local = new FakeStorage()
  local.setItem('alisten.member-token', 'token')
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const request = `${init.method} ${path}`
    requests.push(request)
    if (request === 'GET /api/me/playlists')
      return read({ items: [...serverPlaylists] })
    if (request === 'POST /api/me/playlist-memberships') {
      const { track } = JSON.parse(String(init.body)) as { track: Track }
      return read({ playlistIds: [...server].filter(([, tracks]) => tracks.has(key(track))).map(([id]) => id) })
    }
    // 让测试控制请求完成的先后，模拟连续点击。
    await new Promise<void>(resolve => release.push(resolve))
    if (request === 'DELETE /api/me/playlists/p2') {
      serverPlaylists = serverPlaylists.filter(p => p.id !== 'p2')
      return new Response(null, { status: 204 })
    }
    if (request === 'POST /api/me/playlists') {
      serverPlaylists.push(commute)
      return json(201, commute)
    }
    if (init.body)
      bodies.push(JSON.parse(String(init.body)))
    if (importFailure && /^POST \/api\/me\/(?:playlists\/(?:imports|platform-imports)|playlist-links)$/.test(request))
      return json(importFailure.status, { error: { code: importFailure.code, message: importFailure.message } })
    if (request === 'POST /api/me/playlists/imports' || request === 'POST /api/me/playlists/platform-imports') {
      serverPlaylists.push(imported)
      return json(201, { playlist: imported, imported: 2 })
    }
    if (request === 'POST /api/me/playlist-links')
      return json(200, { source: 'netease', playlistId: '3778678' })
    const write = /^POST \/api\/me\/playlists\/(\w+)\/(items|removals|copies|moves)$/.exec(request)
    if (write?.[2] === 'items') {
      if (failAdd)
        return json(409, { error: { code: 'CONFLICT', message: notReady } })
      const { track } = JSON.parse(String(init.body)) as { track: Track }
      items(write[1]).set(key(track), track)
      return json(201, { item: { track, meta } })
    }
    if (write?.[2] === 'removals') {
      if (failRemove)
        return json(503, { error: { code: 'UNAVAILABLE', message: '暂时不可用' } })
      const { tracks } = JSON.parse(String(init.body)) as { tracks: Track[] }
      for (const track of tracks)
        items(write[1]).delete(key(track))
      return new Response(null, { status: 204 })
    }
    if (write?.[2] === 'copies') {
      if (failCopy)
        return json(409, { error: { code: 'CONFLICT', message: full } })
      const { tracks } = JSON.parse(String(init.body)) as { tracks: Track[] }
      const target = items(write[1])
      const added = tracks.filter(track => !target.has(key(track)))
      for (const track of added)
        target.set(key(track), track)
      return json(200, { added: added.length })
    }
    if (write?.[2] === 'moves') {
      if (failMove)
        return json(409, { error: { code: 'CONFLICT', message: changed } })
      return new Response(null, { status: 204 })
    }
    throw new Error(`没有预期的请求 ${request}`)
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

async function loaded() {
  const { playlistsStore } = await import('./playlists-store')
  await playlistsStore.load()
  await playlistsStore.containing(sunny)
  await playlistsStore.containing(rain)
  requests = []
  const included = (track: Track) => playlistsStore.state.memberships.get(key(track))?.has('p1') ?? false
  return { playlistsStore, included }
}

function flush() {
  return new Promise(resolve => setTimeout(resolve, 0))
}

// 每次先等一个宏任务，让已经开始的请求走到 fetch，再放行最早的一个。
async function settle() {
  for (;;) {
    await new Promise(resolve => setTimeout(resolve, 0))
    const next = release.shift()
    if (!next)
      return
    next()
  }
}

const writes = () => requests.filter(r => !r.startsWith('GET ') && r !== 'POST /api/me/playlist-memberships')

describe('playlistsStore', () => {
  it('load 读取歌单', async () => {
    const { playlistsStore } = await import('./playlists-store')
    await playlistsStore.load()
    expect(playlistsStore.state.playlists).toEqual([favorites])
    expect(requests).toEqual(['GET /api/me/playlists'])
  })

  it('每次读到歌单都记下读取的时间，相对日期以它为准', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    try {
      vi.setSystemTime(new Date('2026-01-01T23:59:00'))
      const { playlistsStore } = await import('./playlists-store')
      await playlistsStore.load()
      const before = playlistsStore.state.loadedAt
      vi.setSystemTime(new Date('2026-01-02T00:01:00'))
      await playlistsStore.load()
      expect(before).toBe(new Date('2026-01-01T23:59:00').getTime())
      expect(playlistsStore.state.loadedAt).toBe(new Date('2026-01-02T00:01:00').getTime())
    }
    finally {
      vi.useRealTimers()
    }
  })

  it('containing 读取包含这首歌的歌单', async () => {
    const { included } = await loaded()
    expect(included(sunny)).toBe(true)
    expect(included(rain)).toBe(false)
  })

  it('加入请求进行中又取消勾选，完成后再发移出', async () => {
    const { playlistsStore, included } = await loaded()
    void playlistsStore.setMembership(favorites, rain, true)
    void playlistsStore.setMembership(favorites, rain, false)
    await settle()
    expect(writes()).toEqual(['POST /api/me/playlists/p1/items', 'POST /api/me/playlists/p1/removals'])
    expect(included(rain)).toBe(false)
  })

  it('连点三次只发一次加入', async () => {
    const { playlistsStore, included } = await loaded()
    void playlistsStore.setMembership(favorites, rain, true)
    void playlistsStore.setMembership(favorites, rain, false)
    void playlistsStore.setMembership(favorites, rain, true)
    await settle()
    expect(writes()).toEqual(['POST /api/me/playlists/p1/items'])
    expect(included(rain)).toBe(true)
  })

  // 同步结束后会重新读取，读到的服务端状态也能把勾选改对。下面几个用例挡住这次读取，只看回滚本身。
  it('第一次请求失败时以服务端为准，提示服务端的文案', async () => {
    const { playlistsStore, included } = await loaded()
    const { notificationStore } = await import('@/shared/ui')
    failAdd = true
    holdReads = true
    const done = playlistsStore.setMembership(favorites, rain, true)
    void playlistsStore.setMembership(favorites, rain, false)
    void playlistsStore.setMembership(favorites, rain, true)
    await flush()
    release.shift()!()
    await done
    expect(writes()).toEqual(['POST /api/me/playlists/p1/items'])
    expect(included(rain)).toBe(false)
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual([notReady])
    holdReads = false
    await settle()
    expect(included(rain)).toBe(false)
  })

  it('移出失败时恢复勾选', async () => {
    const { playlistsStore, included } = await loaded()
    failRemove = true
    holdReads = true
    const done = playlistsStore.setMembership(favorites, sunny, false)
    expect(included(sunny)).toBe(false)
    await flush()
    release.shift()!()
    await done
    expect(included(sunny)).toBe(true)
    holdReads = false
    await settle()
    expect(included(sunny)).toBe(true)
  })

  it('加入成功后移出失败，勾选恢复为已加入', async () => {
    const { playlistsStore, included } = await loaded()
    failRemove = true
    holdReads = true
    const done = playlistsStore.setMembership(favorites, rain, true)
    void playlistsStore.setMembership(favorites, rain, false)
    await flush()
    release.shift()!()
    await flush()
    release.shift()!()
    await done
    expect(writes()).toEqual(['POST /api/me/playlists/p1/items', 'POST /api/me/playlists/p1/removals'])
    expect(included(rain)).toBe(true)
    holdReads = false
    await settle()
    expect(included(rain)).toBe(true)
  })

  it('移出失败后重新读取也失败，勾选保持回滚的结果', async () => {
    const { playlistsStore, included } = await loaded()
    failRemove = true
    failReads = true
    const done = playlistsStore.setMembership(favorites, sunny, false)
    await settle()
    await done
    expect(requests).toEqual(['POST /api/me/playlists/p1/removals', 'POST /api/me/playlist-memberships', 'GET /api/me/playlists'])
    expect(included(sunny)).toBe(true)
    expect(playlistsStore.state.error).toBe('暂时不可用')
  })

  it('读取期间正在加入的歌以本地为准', async () => {
    const { playlistsStore, included } = await loaded()
    void playlistsStore.setMembership(favorites, rain, true)
    await flush()
    await playlistsStore.containing(rain)
    expect(included(rain)).toBe(true)
    await settle()
    expect(included(rain)).toBe(true)
  })

  it('读取期间正在移出的歌以本地为准', async () => {
    const { playlistsStore, included } = await loaded()
    void playlistsStore.setMembership(favorites, sunny, false)
    await flush()
    await playlistsStore.containing(sunny)
    expect(included(sunny)).toBe(false)
    await settle()
    expect(included(sunny)).toBe(false)
  })

  it('同步完成之后才返回的旧读取不会取消勾选', async () => {
    const { playlistsStore, included } = await loaded()
    holdReads = true
    void playlistsStore.setMembership(favorites, rain, true)
    await flush()
    void playlistsStore.containing(rain)
    await flush()
    // 先放行加入，同步结束；再放行加入之前发出的读取。
    release.shift()!()
    await flush()
    release.shift()!()
    await flush()
    expect(included(rain)).toBe(true)
    holdReads = false
    await settle()
    expect(included(rain)).toBe(true)
  })

  it('同步结束后重新读取歌单和这首歌所在的歌单', async () => {
    const { playlistsStore } = await loaded()
    void playlistsStore.setMembership(favorites, rain, true)
    await settle()
    expect(requests).toEqual(['POST /api/me/playlists/p1/items', 'POST /api/me/playlist-memberships', 'GET /api/me/playlists'])
  })

  it('removeItems 一次移出多首，成功后重新读取歌单', async () => {
    const { playlistsStore } = await loaded()
    items('p1').set(key(rain), rain)
    const done = playlistsStore.removeItems(favorites, [sunny, rain])
    await settle()
    expect(await done).toBe(true)
    expect(requests).toEqual(['POST /api/me/playlists/p1/removals', 'GET /api/me/playlists'])
    expect(bodies).toEqual([{ tracks: [sunny, rain] }])
    expect(items('p1').size).toBe(0)
  })

  it('removeItems 失败时提示并返回 false', async () => {
    const { playlistsStore } = await loaded()
    const { notificationStore } = await import('@/shared/ui')
    failRemove = true
    const done = playlistsStore.removeItems(favorites, [sunny])
    await settle()
    expect(await done).toBe(false)
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual(['暂时不可用'])
  })

  it('copyItems 返回新加入的首数，目标歌单里已有的歌不算', async () => {
    const { playlistsStore } = await loaded()
    items('p2').set(key(sunny), sunny)
    const done = playlistsStore.copyItems(favorites, commute, [sunny, rain])
    await settle()
    expect(await done).toBe(1)
    expect(requests).toEqual(['POST /api/me/playlists/p2/copies', 'GET /api/me/playlists'])
    expect(bodies).toEqual([{ from: 'p1', tracks: [sunny, rain] }])
  })

  it('copyItems 装不下时提示是哪个歌单并返回 null', async () => {
    const { playlistsStore } = await loaded()
    const { notificationStore } = await import('@/shared/ui')
    failCopy = true
    const done = playlistsStore.copyItems(favorites, commute, [sunny])
    await settle()
    expect(await done).toBeNull()
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual([`没能保存到“通勤”：${full}`])
  })

  it('moveItems 有 before 时放到它之前，没有时移到最后', async () => {
    const { playlistsStore } = await loaded()
    const first = playlistsStore.moveItems(favorites, [rain], sunny)
    await settle()
    const last = playlistsStore.moveItems(favorites, [sunny])
    await settle()
    expect([await first, await last]).toEqual([true, true])
    expect(writes()).toEqual(['POST /api/me/playlists/p1/moves', 'POST /api/me/playlists/p1/moves'])
    expect(bodies).toEqual([{ tracks: [rain], before: sunny }, { tracks: [sunny] }])
    expect(requests.filter(r => r === 'GET /api/me/playlists')).toHaveLength(2)
  })

  it('moveItems 失败时提示并返回 false', async () => {
    const { playlistsStore } = await loaded()
    const { notificationStore } = await import('@/shared/ui')
    failMove = true
    const done = playlistsStore.moveItems(favorites, [sunny])
    await settle()
    expect(await done).toBe(false)
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual([changed])
  })

  // 详情页的用法：移动经过 TrackEdits 排队。CONFLICT 说明歌单在别处变了，排在后面的移动基于过时的顺序。
  it('排队的移动遇到 CONFLICT：提示服务端的文案，回到这次移动之前，后面的不再发出，结束后重新读取', async () => {
    const { playlistsStore } = await loaded()
    const { notificationStore } = await import('@/shared/ui')
    const { TrackEdits } = await import('./track-edits')
    let rereads = 0
    const edits = new TrackEdits(() => rereads++)
    failMove = true
    let shown = '第一次移动之后'
    void edits.run(() => playlistsStore.moveItems(favorites, [rain], sunny), () => {
      shown = '移动之前'
    })
    shown = '第二次移动之后'
    const done = edits.run(() => playlistsStore.moveItems(favorites, [sunny]), () => {
      shown = '第一次移动之后'
    })
    await settle()
    await done
    expect(writes()).toEqual(['POST /api/me/playlists/p1/moves'])
    expect(shown).toBe('移动之前')
    expect(rereads).toBe(1)
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual([changed])
  })

  it('游客不读取歌单', async () => {
    const { playlistsStore } = await import('./playlists-store')
    playlistsStore.start()
    window.dispatchEvent(new Event('focus'))
    await flush()
    expect(requests).toEqual([])
  })

  it('注册之后读取，窗口获得焦点时刷新', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { profileStore } = await import('@/features/settings')
    playlistsStore.start()
    await profileStore.registered({ username: 'alice' })
    await flush()
    expect(playlistsStore.state.playlists).toEqual([favorites])
    window.dispatchEvent(new Event('focus'))
    await flush()
    expect(requests.filter(r => r === 'GET /api/me/playlists')).toHaveLength(2)
  })

  it('换账号之后，上一个账号的读取结果作废', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { profileStore } = await import('@/features/settings')
    holdReads = true
    playlistsStore.start()
    await profileStore.registered({ username: 'alice' })
    await profileStore.registered({ username: 'bob' })
    await flush()
    release.shift()!()
    await flush()
    expect(playlistsStore.state.playlists).toBeNull()
    release.shift()!()
    await flush()
    expect(playlistsStore.state.playlists).toEqual([favorites])
  })

  it('换账号之后，上一个账号未完成的保存失败也不提示', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { profileStore } = await import('@/features/settings')
    const { notificationStore } = await import('@/shared/ui')
    playlistsStore.start()
    await profileStore.registered({ username: 'alice' })
    await flush()
    failAdd = true
    const done = playlistsStore.setMembership(favorites, rain, true)
    await flush()
    await profileStore.registered({ username: 'bob' })
    release.shift()!()
    await done
    expect(notificationStore.state.notifications).toEqual([])
  })

  it('换账号之后，上一个账号读到的所在歌单作废', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { profileStore } = await import('@/features/settings')
    playlistsStore.start()
    await profileStore.registered({ username: 'alice' })
    await flush()
    holdReads = true
    const reading = playlistsStore.containing(sunny)
    await flush()
    await profileStore.registered({ username: 'bob' })
    release.shift()!()
    await reading
    expect(playlistsStore.state.memberships.size).toBe(0)
  })

  // 读取在删除或新建之前到达服务端，却在之后才返回时，结果不能写回界面。
  it('读取进行中删除歌单，旧读取返回后歌单不会回来', async () => {
    serverPlaylists.push(commute)
    const { playlistsStore } = await loaded()
    holdReads = true
    void playlistsStore.load()
    await flush()
    const removed = playlistsStore.remove(commute)
    await flush()
    release.splice(1, 1)[0]()
    await removed
    holdReads = false
    await settle()
    expect(playlistsStore.state.playlists).toEqual([favorites])
  })

  it('读取进行中新建歌单，旧读取返回后歌单不会消失', async () => {
    const { playlistsStore } = await loaded()
    holdReads = true
    void playlistsStore.load()
    await flush()
    const created = playlistsStore.create('通勤')
    await flush()
    release.splice(1, 1)[0]()
    await created
    holdReads = false
    await settle()
    expect(playlistsStore.state.playlists).toEqual([favorites, commute])
  })

  it('importFile 导入成功后新歌单立刻出现在列表里，再重新读取', async () => {
    const { playlistsStore } = await import('./playlists-store')
    await playlistsStore.load()
    requests = []
    holdReads = true
    const done = playlistsStore.importFile('通勤', [fileTrack])
    await flush()
    release.shift()!()
    expect(await done).toEqual({ playlist: imported, imported: 2 })
    expect(playlistsStore.state.playlists).toEqual([favorites, imported])
    expect(bodies).toEqual([{ name: '通勤', tracks: [fileTrack] }])
    holdReads = false
    await settle()
    expect(requests).toEqual(['POST /api/me/playlists/imports', 'GET /api/me/playlists'])
    expect(playlistsStore.state.playlists).toEqual([favorites, imported])
  })

  it('importFile 遇到 INVALID_ARGUMENT 时说是文件的问题，不重新读取', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { notificationStore } = await import('@/shared/ui')
    importFailure = { status: 400, code: 'INVALID_ARGUMENT', message: 'tracks[0].track.id：不是合法的链接' }
    const done = playlistsStore.importFile('通勤', [fileTrack])
    await settle()
    expect(await done).toBeNull()
    expect(requests).toEqual(['POST /api/me/playlists/imports'])
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual(['文件里有不符合要求的内容，没有导入。'])
  })

  it('importFile 超过歌单数上限时提示服务端的文案', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { notificationStore } = await import('@/shared/ui')
    importFailure = { status: 409, code: 'CONFLICT', message: '最多只能建 50 个歌单' }
    const done = playlistsStore.importFile('通勤', [fileTrack])
    await settle()
    expect(await done).toBeNull()
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual(['最多只能建 50 个歌单'])
  })

  it('resolveLink 返回音源和歌单 ID，不重新读取歌单', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const link = '分享歌单：通勤 https://163cn.tv/abc'
    const done = playlistsStore.resolveLink(link)
    await settle()
    expect(await done).toEqual({ source: 'netease', playlistId: '3778678' })
    expect(requests).toEqual(['POST /api/me/playlist-links'])
    expect(bodies).toEqual([{ link }])
  })

  it('resolveLink 认不出链接时提示服务端的文案', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { notificationStore } = await import('@/shared/ui')
    importFailure = { status: 400, code: 'INVALID_ARGUMENT', message: '不是网易云或 QQ 音乐的歌单链接' }
    const done = playlistsStore.resolveLink('https://example.com/')
    await settle()
    expect(await done).toBeNull()
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual(['不是网易云或 QQ 音乐的歌单链接'])
  })

  it('resolveLink 超过 2048 字节的文本不发出', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { notificationStore } = await import('@/shared/ui')
    expect(await playlistsStore.resolveLink('歌'.repeat(683))).toBeNull()
    expect(requests).toEqual([])
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual(['文字太长了，只粘贴歌单的链接或分享文字就好。'])
    const done = playlistsStore.resolveLink('歌'.repeat(682))
    await settle()
    await done
    expect(requests).toEqual(['POST /api/me/playlist-links'])
  })

  it('importPlatform 导入成功后新歌单立刻出现在列表里，再重新读取', async () => {
    const { playlistsStore } = await import('./playlists-store')
    holdReads = true
    const done = playlistsStore.importPlatform('通勤', 'netease', '3778678')
    await flush()
    release.shift()!()
    expect(await done).toEqual({ playlist: imported, imported: 2 })
    expect(playlistsStore.state.playlists).toEqual([imported])
    expect(bodies).toEqual([{ name: '通勤', source: 'netease', playlistId: '3778678' }])
    holdReads = false
    await settle()
    expect(requests).toEqual(['POST /api/me/playlists/platform-imports', 'GET /api/me/playlists'])
    expect(playlistsStore.state.playlists).toEqual([favorites, imported])
  })

  it('importPlatform 歌单名不合法时说明规则，其他错误提示服务端的文案', async () => {
    const { playlistsStore } = await import('./playlists-store')
    const { notificationStore } = await import('@/shared/ui')
    importFailure = { status: 400, code: 'INVALID_ARGUMENT', message: '歌单名不合法' }
    const first = playlistsStore.importPlatform(' ', 'netease', '3778678')
    await settle()
    importFailure = { status: 429, code: 'RATE_LIMITED', message: '导入太频繁了，稍后再试' }
    const second = playlistsStore.importPlatform('通勤', 'netease', '3778678')
    await settle()
    expect([await first, await second]).toEqual([null, null])
    expect(notificationStore.state.notifications.map(n => n.message)).toEqual(['歌单名要 1 到 50 个字。', '导入太频繁了，稍后再试'])
  })
})
