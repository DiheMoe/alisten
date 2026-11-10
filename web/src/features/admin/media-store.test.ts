import type { AdminListMediaRequest, AdminRemoveMediaRequest } from '@/core/api'
import type { StoredMedia } from '@/core/realtime'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { notificationStore } from '@/shared/ui'
import { adminStore } from './admin-store'
import { fallbackPage, MediaStore } from './media-store'

class FakeStorage {
  readonly #items = new Map<string, string>()

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

function media(n: number): StoredMedia {
  return { key: `k${n}`, source: 'link', link: `https://example.com/${n}.mp3`, title: `歌 ${n}`, artist: '', coverUrl: '', size: n * 1000, storedAtMs: n, lastPlayedAtMs: null }
}

let library: StoredMedia[]
let playing: Set<string>
let reads: AdminListMediaRequest[]
let removals: string[][]
// holdReads 时读取的结果在请求到达时确定，等 release 之后才返回，模拟慢的读取。
let holdReads: boolean
let release: (() => void)[]
let failReads: boolean
let failRemovals: boolean

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status })
}

const unavailable = { error: { code: 'UNAVAILABLE', message: '服务暂时不可用，请稍后再试。' } }

async function list(body: AdminListMediaRequest): Promise<Response> {
  reads.push(body)
  const page = body.page ?? 1
  const matched = library.filter(item => item.title.includes(body.q ?? ''))
  const response = json(200, {
    items: matched.slice((page - 1) * 50, page * 50),
    total: matched.length,
    totalBytes: matched.reduce((sum, item) => sum + item.size, 0),
    page,
    pageSize: 50,
  })
  if (holdReads)
    await new Promise<void>(resolve => release.push(resolve))
  return failReads ? json(503, unavailable) : response
}

function remove(body: AdminRemoveMediaRequest): Response {
  const keys = body.media.map(ref => ref.key)
  removals.push(keys)
  const inUse = keys.filter(key => playing.has(key))
  const deleted = keys.filter(key => !playing.has(key) && library.some(item => item.key === key))
  // 中途出错时，出错之前删掉的不会恢复。
  const done = failRemovals ? deleted.slice(0, -1) : deleted
  library = library.filter(item => !done.includes(item.key))
  return failRemovals ? json(503, unavailable) : json(200, { deleted, inUse })
}

const keys = (store: MediaStore) => store.state.list?.items.map(item => item.key)
const selected = (store: MediaStore) => [...store.state.selected].sort()
const messages = () => notificationStore.state.notifications.map(n => n.message)

// 等作废的读取走完。作废的读取不改变任何状态，所以没有可以等待的信号。
const drain = () => new Promise(resolve => setTimeout(resolve, 20))

function releaseAll(): void {
  for (const resolve of release.splice(0))
    resolve()
}

beforeEach(() => {
  library = Array.from({ length: 55 }, (_, index) => media(index + 1))
  playing = new Set()
  reads = []
  removals = []
  holdReads = false
  release = []
  failReads = false
  failRemovals = false
  vi.stubGlobal('sessionStorage', new FakeStorage())
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const body: unknown = JSON.parse(init.body as string)
    if (path === '/api/admin/media/queries')
      return list(body as AdminListMediaRequest)
    if (path === '/api/admin/media/removals')
      return remove(body as AdminRemoveMediaRequest)
    return json(404, { error: { code: 'NOT_FOUND', message: '接口不存在' } })
  })
  for (const { id } of notificationStore.state.notifications)
    notificationStore.dismiss(id)
  adminStore.signIn('token')
})

describe('mediaStore 的读取', () => {
  it('按地址里的参数读取一页', async () => {
    const store = new MediaStore()
    store.show({ q: '歌 1', sort: 'size', page: 1 })
    expect(store.state.loading).toBe(true)
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    expect(reads).toEqual([{ q: '歌 1', sort: 'size', page: 1 }])
    expect(store.state.list).toMatchObject({ total: 11, page: 1, pageSize: 50 })
    expect(store.state.error).toBeNull()
  })

  it('同一组参数只请求一次', async () => {
    const store = new MediaStore()
    store.show({ q: '', sort: 'stored', page: 1 })
    store.show({ q: '', sort: 'stored', page: 1 })
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    store.show({ q: '', sort: 'stored', page: 1 })
    expect(reads).toHaveLength(1)
    store.show({ q: '', sort: 'stored', page: 2 })
    store.show({ q: '', sort: 'stored', page: 2 })
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    expect(reads.map(read => read.page)).toEqual([1, 2])
  })

  it('参数变化时，先发出的读取晚回来也不覆盖新的一页', async () => {
    const store = new MediaStore()
    holdReads = true
    store.show({ q: '', sort: 'stored', page: 1 })
    store.show({ q: '', sort: 'stored', page: 2 })
    await vi.waitFor(() => expect(release).toHaveLength(2))
    const [first, second] = release.splice(0)
    second!()
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    expect(keys(store)).toEqual(['k51', 'k52', 'k53', 'k54', 'k55'])
    first!()
    await drain()
    expect(store.state.list?.page).toBe(2)
    expect(store.state.loading).toBe(false)
  })

  it('新的一页读到之前保留上一页', async () => {
    const store = new MediaStore()
    store.show({ q: '', sort: 'stored', page: 1 })
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    holdReads = true
    store.show({ q: '', sort: 'stored', page: 2 })
    expect(store.state.loading).toBe(true)
    expect(store.state.list?.page).toBe(1)
    await vi.waitFor(() => expect(release).toHaveLength(1))
    releaseAll()
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    expect(store.state.list?.page).toBe(2)
  })

  it('读取失败时就地给出原因，不弹通知；重试时即使参数没变也重新读取', async () => {
    const store = new MediaStore()
    failReads = true
    store.show({ q: '', sort: 'stored', page: 1 })
    await vi.waitFor(() => expect(store.state.error).toBe('服务暂时不可用，请稍后再试。'))
    expect(store.state.loading).toBe(false)
    expect(messages()).toEqual([])
    failReads = false
    store.retry()
    expect(store.state.error).toBeNull()
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    expect(reads).toHaveLength(2)
    expect(store.state.list?.items).toHaveLength(50)
  })
})

describe('mediaStore 的选择', () => {
  async function opened(page = 1): Promise<MediaStore> {
    const store = new MediaStore()
    store.show({ q: '', sort: 'stored', page })
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    return store
  }

  it('逐个切换，按住 Shift 选中上一次点的到这一项之间的全部', async () => {
    const store = await opened()
    store.toggle('k2', false)
    store.toggle('k5', true)
    expect(selected(store)).toEqual(['k2', 'k3', 'k4', 'k5'])
    store.toggle('k3', false)
    expect(selected(store)).toEqual(['k2', 'k4', 'k5'])
  })

  it('表头的复选框全选本页，再点一次全部取消', async () => {
    const store = await opened(2)
    store.toggle('k52', false)
    store.toggleAll()
    expect(selected(store)).toEqual(['k51', 'k52', 'k53', 'k54', 'k55'])
    store.toggleAll()
    expect(selected(store)).toEqual([])
  })

  it('换了参数就清空选择，Shift 也不再从上一页选起', async () => {
    const store = await opened()
    store.toggle('k1', false)
    store.show({ q: '', sort: 'stored', page: 2 })
    expect(selected(store)).toEqual([])
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    store.toggle('k53', true)
    expect(selected(store)).toEqual(['k53'])
  })

  it('取消选择', async () => {
    const store = await opened()
    store.toggle('k1', false)
    store.clearSelection()
    expect(selected(store)).toEqual([])
  })
})

describe('mediaStore 的删除', () => {
  async function opened(page = 1): Promise<MediaStore> {
    const store = new MediaStore()
    store.show({ q: '', sort: 'stored', page })
    await vi.waitFor(() => expect(store.state.loading).toBe(false))
    return store
  }

  it('删除之后重新读取当前页，读完才返回', async () => {
    const store = await opened()
    const result = await store.remove(['k1', 'k2'])
    expect(result).toEqual({ deleted: ['k1', 'k2'], inUse: [] })
    expect(removals).toEqual([['k1', 'k2']])
    expect(reads.map(read => read.page)).toEqual([1, 1])
    expect(store.state.loading).toBe(false)
    expect(keys(store)?.slice(0, 2)).toEqual(['k3', 'k4'])
    expect(store.state.list?.total).toBe(53)
  })

  it('正在房间里播放的歌原样交给界面，它们留在这一页上，也仍然选中', async () => {
    playing = new Set(['k2'])
    const store = await opened()
    store.toggle('k1', false)
    store.toggle('k3', true)
    const result = await store.remove([...store.state.selected])
    expect(result).toEqual({ deleted: ['k1', 'k3'], inUse: ['k2'] })
    expect(keys(store)?.slice(0, 2)).toEqual(['k2', 'k4'])
    expect(selected(store)).toEqual(['k2'])
  })

  it('当前页删空、又不是第 1 页时，回到上一页', async () => {
    const store = await opened(2)
    await store.remove(['k51', 'k52', 'k53', 'k54', 'k55'])
    expect(reads.map(read => read.page)).toEqual([2, 2])
    expect(store.state.list?.items).toEqual([])
    expect(fallbackPage(store.state.list!)).toBe(1)
  })

  it('中途出错时提示原因，也重新读取当前页：出错之前删掉的歌不再显示，选择里也去掉', async () => {
    const store = await opened()
    store.toggle('k1', false)
    store.toggle('k3', true)
    failRemovals = true
    expect(await store.remove([...store.state.selected])).toBeNull()
    expect(messages()).toEqual(['服务暂时不可用，请稍后再试。'])
    expect(reads.map(read => read.page)).toEqual([1, 1])
    expect(keys(store)?.slice(0, 2)).toEqual(['k3', 'k4'])
    expect(store.state.list?.total).toBe(53)
    expect(selected(store)).toEqual(['k3'])
  })

  it('删除之前发出、还没回来的读取作废', async () => {
    const store = await opened()
    holdReads = true
    store.retry()
    await vi.waitFor(() => expect(release).toHaveLength(1))
    const stale = release.shift()!
    const removing = store.remove(['k1'])
    await vi.waitFor(() => expect(release).toHaveLength(1))
    releaseAll()
    await removing
    stale()
    await drain()
    expect(keys(store)?.[0]).toBe('k2')
  })
})

describe('fallbackPage', () => {
  const page = (items: number, total: number, at: number) => ({ items: Array.from({ length: items }, (_, index) => media(index)), total, totalBytes: 0, page: at, pageSize: 50 })

  it('这一页有内容，或者已经是第 1 页时不用换页', () => {
    expect(fallbackPage(page(5, 55, 2))).toBeNull()
    expect(fallbackPage(page(0, 0, 1))).toBeNull()
  })

  it('空页改去最后一页有内容的页，全都没有时去第 1 页', () => {
    expect(fallbackPage(page(0, 100, 3))).toBe(2)
    expect(fallbackPage(page(0, 101, 9))).toBe(3)
    expect(fallbackPage(page(0, 0, 4))).toBe(1)
  })
})
