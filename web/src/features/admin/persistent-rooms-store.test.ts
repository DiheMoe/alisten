import type { RoomListing } from '@/core/realtime'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeStorage } from '@/core/api/fake-storage'
import { notificationStore } from '@/shared/lib/notification-store'
import { adminStore } from './admin-store'
import { PersistentRoomsStore } from './persistent-rooms-store'

function room(n: number): RoomListing {
  return { id: `r${n}`, slug: null, name: `房间 ${n}`, description: '', kind: 'persistent', hasPassword: false, onlineCount: 0, capacity: 64 }
}

let server: RoomListing[]
let requests: string[]
// holdReads 时读取的结果在请求到达时确定，等 release 之后才返回，模拟慢的读取。
let holdReads: boolean
let release: (() => void)[]
let failReads: boolean
let failDeletes: boolean

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status })
}

const unavailable = { error: { code: 'UNAVAILABLE', message: '服务暂时不可用，请稍后再试。' } }

async function list(): Promise<Response> {
  const response = failReads ? json(503, unavailable) : json(200, { items: [...server], defaultCapacity: 32 })
  if (holdReads)
    await new Promise<void>(resolve => release.push(resolve))
  return response
}

function remove(id: string): Response {
  if (failDeletes)
    return json(503, unavailable)
  server = server.filter(item => item.id !== id)
  return new Response(null, { status: 204 })
}

function ids(store: PersistentRoomsStore): string[] | string {
  const { current } = store.rooms.state
  return current.status === 'loaded' ? current.value.items.map(item => item.id) : current.status
}
const messages = () => notificationStore.state.notifications.map(n => n.message)

// 等作废的读取走完。作废的读取不改变任何状态，所以没有可以等待的信号。
const drain = () => new Promise(resolve => setTimeout(resolve, 20))

function releaseAll(): void {
  for (const resolve of release.splice(0))
    resolve()
}

async function opened(): Promise<PersistentRoomsStore> {
  const store = new PersistentRoomsStore()
  store.open()
  await vi.waitFor(() => expect(store.rooms.state.current.status).toBe('loaded'))
  requests = []
  return store
}

beforeEach(() => {
  server = [room(1), room(2), room(3)]
  requests = []
  holdReads = false
  release = []
  failReads = false
  failDeletes = false
  vi.stubGlobal('sessionStorage', new FakeStorage())
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const request = `${init.method} ${path}`
    requests.push(request)
    if (request === 'GET /api/admin/rooms')
      return list()
    const deleted = /^DELETE \/api\/admin\/rooms\/(.+)$/.exec(request)
    if (deleted)
      return remove(decodeURIComponent(deleted[1]!))
    return json(404, { error: { code: 'NOT_FOUND', message: '接口不存在' } })
  })
  for (const { id } of notificationStore.state.notifications)
    notificationStore.dismiss(id)
  adminStore.signIn('token')
})

describe('常驻房间的读取', () => {
  it('打开时读取全部房间和新建房间的默认上限', async () => {
    const store = new PersistentRoomsStore()
    store.open()
    expect(store.rooms.state.current).toEqual({ status: 'loading' })
    await vi.waitFor(() => expect(ids(store)).toEqual(['r1', 'r2', 'r3']))
    expect(store.rooms.state.current).toMatchObject({ value: { defaultCapacity: 32 } })
    expect(requests).toEqual(['GET /api/admin/rooms'])
  })

  it('读取失败时就地给出原因，不弹通知，重新读取时先回到读取中', async () => {
    failReads = true
    const store = new PersistentRoomsStore()
    store.open()
    await vi.waitFor(() => expect(store.rooms.state.current).toEqual({ status: 'failed', error: '服务暂时不可用，请稍后再试。' }))
    expect(messages()).toEqual([])
    failReads = false
    store.refresh()
    expect(store.rooms.state.current).toEqual({ status: 'loading' })
    await vi.waitFor(() => expect(ids(store)).toEqual(['r1', 'r2', 'r3']))
  })

  it('刷新期间继续显示现在的列表', async () => {
    const store = await opened()
    server.push(room(4))
    holdReads = true
    store.refresh()
    expect(ids(store)).toEqual(['r1', 'r2', 'r3'])
    await vi.waitFor(() => expect(release).toHaveLength(1))
    releaseAll()
    await vi.waitFor(() => expect(ids(store)).toEqual(['r1', 'r2', 'r3', 'r4']))
  })

  it('关闭之后，还没返回的读取作废', async () => {
    holdReads = true
    const store = new PersistentRoomsStore()
    store.open()
    await vi.waitFor(() => expect(release).toHaveLength(1))
    store.close()
    releaseAll()
    await drain()
    expect(store.rooms.state.current).toEqual({ status: 'loading' })
  })
})

describe('常驻房间的删除', () => {
  it('删除之后先在本地去掉这个房间，再重新读取', async () => {
    const store = await opened()
    holdReads = true
    // 服务端多了一个房间，本地的列表里还没有。
    server.push(room(4))
    expect(await store.remove('r2')).toBe(true)
    expect(ids(store)).toEqual(['r1', 'r3'])
    expect(store.rooms.state.current).toMatchObject({ value: { defaultCapacity: 32 } })
    await vi.waitFor(() => expect(release).toHaveLength(1))
    expect(requests).toEqual(['DELETE /api/admin/rooms/r2', 'GET /api/admin/rooms'])
    releaseAll()
    await vi.waitFor(() => expect(ids(store)).toEqual(['r1', 'r3', 'r4']))
  })

  it('失败时提示原因，列表不变，也不重新读取', async () => {
    const store = await opened()
    failDeletes = true
    expect(await store.remove('r2')).toBe(false)
    expect(messages()).toEqual(['服务暂时不可用，请稍后再试。'])
    expect(ids(store)).toEqual(['r1', 'r2', 'r3'])
    expect(requests).toEqual(['DELETE /api/admin/rooms/r2'])
  })

  it('删除之前发出、还没回来的读取作废', async () => {
    const store = await opened()
    holdReads = true
    store.refresh()
    await vi.waitFor(() => expect(release).toHaveLength(1))
    const stale = release.shift()!
    expect(await store.remove('r1')).toBe(true)
    await vi.waitFor(() => expect(release).toHaveLength(1))
    stale()
    await drain()
    expect(ids(store)).toEqual(['r2', 'r3'])
    releaseAll()
  })

  it('列表没有读到时删除成功，直接重新读取', async () => {
    failReads = true
    const store = new PersistentRoomsStore()
    store.open()
    await vi.waitFor(() => expect(store.rooms.state.current.status).toBe('failed'))
    failReads = false
    holdReads = true
    expect(await store.remove('r1')).toBe(true)
    expect(ids(store)).toBe('loading')
    await vi.waitFor(() => expect(release).toHaveLength(1))
    releaseAll()
    await vi.waitFor(() => expect(ids(store)).toEqual(['r2', 'r3']))
  })
})
