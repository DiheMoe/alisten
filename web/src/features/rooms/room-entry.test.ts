import type { RoomListing, RoomSnapshot } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeStorage } from '@/core/api/fake-storage'
import { CloseCode } from '@/core/realtime'
import { FakeSocket, socketAt, sockets, stubBrowser } from '@/core/realtime/fake-socket'

const memberTokenKey = 'alisten.member-token'
const ticketKey = 'alisten.ticket.r1'
const lastRoomKey = 'alisten.last-room'
const member = { id: 'm1', name: 'alice', avatarHash: '' }
const snapshot: RoomSnapshot = {
  term: 1,
  seq: 0,
  room: { id: 'r1', name: '房间', description: '', kind: 'persistent' },
  playMode: 'sequential',
  nowPlaying: null,
  pending: null,
  items: [],
  members: { sessions: [], onlineCount: 0 },
}

type Handler = (token: string | null) => Response | Promise<Response>

function acceptHello(socket: FakeSocket): void {
  socket.respond(socket.last('hello'), { member, room: snapshot, serverTimeMs: 0 })
}

function listing(patch: Partial<RoomListing> = {}): RoomListing {
  return { id: 'r1', slug: null, name: '房间', description: '', kind: 'persistent', hasPassword: false, onlineCount: 0, ...patch }
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status })
}

function errorResponse(status: number, code: string, message: string): Response {
  return json(status, { error: { code, message } })
}

const found = (room: RoomListing) => () => json(200, room)
const notFound = () => errorResponse(404, 'NOT_FOUND', '房间不存在')
const unavailable = () => errorResponse(503, 'UNAVAILABLE', '服务暂时不可用，请稍后再试。')

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

let routes: Map<string, Handler>
let requests: string[]
// 每个接口最近一次请求的请求体。
let bodies: Map<string, unknown>
let unexpected: string[]
let local: FakeStorage

const flush = () => vi.advanceTimersByTimeAsync(0)

async function load() {
  const { roomEntryStore, joinErrorMessage } = await import('./room-entry')
  const { copyInviteLink } = await import('./invite')
  const { roomStore } = await import('@/core/room')
  const { api } = await import('@/core/api')
  const { notificationStore } = await import('@/shared/ui')
  roomEntryStore.start()
  const notifications = () => notificationStore.state.notifications.map(({ tone, message }) => ({ tone, message }))
  return { roomEntryStore, joinErrorMessage, copyInviteLink, roomStore, api, notifications }
}

async function entered(ref = 'r1') {
  local.setItem(ticketKey, 't1')
  const loaded = await load()
  await loaded.roomEntryStore.enter(ref)
  socketAt(0).open()
  acceptHello(socketAt(0))
  await flush()
  return loaded
}

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
  requests = []
  bodies = new Map()
  unexpected = []
  routes = new Map<string, Handler>([['GET /api/rooms/r1', found(listing())]])
  local = new FakeStorage()
  stubBrowser()
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const route = `${init.method} ${path}`
    requests.push(route)
    if (typeof init.body === 'string')
      bodies.set(route, JSON.parse(init.body))
    const handler = routes.get(route)
    if (!handler) {
      unexpected.push(route)
      return json(500, {})
    }
    return handler(new Headers(init.headers).get('Authorization')?.replace('Bearer ', '') ?? null)
  })
})

afterEach(() => {
  expect(unexpected).toEqual([])
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('进入房间', () => {
  it('有保存的票据时直接用它连接，握手成功才算进入，这时才记下上次进过的房间', async () => {
    local.setItem(ticketKey, 't1')
    routes.set('GET /api/rooms/party', found(listing({ slug: 'party' })))
    const { roomEntryStore } = await load()

    const entering = roomEntryStore.enter('party')
    expect(roomEntryStore.state).toEqual({ phase: 'resolving', ref: 'party', room: null })
    await entering
    expect(roomEntryStore.state).toEqual({ phase: 'joining', ref: 'party', room: listing({ slug: 'party' }) })

    const socket = socketAt(0)
    socket.open()
    // 进房用的是别名，连接和票据认的是房间的 id。
    expect(socket.last('hello').data).toMatchObject({ roomId: 'r1', ticket: 't1' })
    expect(local.getItem(lastRoomKey)).toBeNull()

    acceptHello(socket)
    await flush()
    expect(roomEntryStore.state.phase).toBe('joined')
    expect(local.getItem(lastRoomKey)).toBe('r1')
  })

  it('没有票据时停在 needs-ticket，不连接。票据保存好之后重试，用它加入', async () => {
    routes.set('GET /api/rooms/r1', found(listing({ hasPassword: true })))
    const { roomEntryStore } = await load()
    await roomEntryStore.enter('r1')
    expect(roomEntryStore.state).toEqual({ phase: 'needs-ticket', ref: 'r1', room: listing({ hasPassword: true }) })
    expect(sockets).toHaveLength(0)

    local.setItem(ticketKey, 't1')
    roomEntryStore.retry()
    await flush()
    expect(roomEntryStore.state.phase).toBe('joining')
    socketAt(0).open()
    expect(socketAt(0).last('hello').data).toMatchObject({ ticket: 't1' })
  })

  it('已经拿到房间信息时不再查询', async () => {
    local.setItem(ticketKey, 't1')
    const { roomEntryStore } = await load()
    await roomEntryStore.enter('r1', listing())
    expect(requests).toEqual([])
    expect(roomEntryStore.state.phase).toBe('joining')
  })

  it('已经在这个房间里时再进一次不重连，用 id 或别名都一样', async () => {
    routes.set('GET /api/rooms/party', found(listing({ slug: 'party' })))
    const { roomEntryStore } = await entered('party')
    await roomEntryStore.enter('r1')
    await roomEntryStore.enter('party', listing({ slug: 'party' }))
    expect(sockets).toHaveLength(1)
    expect(socketAt(0).readyState).toBe(FakeSocket.OPEN)
    expect(requests).toEqual(['GET /api/rooms/party'])
    expect(roomEntryStore.state).toMatchObject({ phase: 'joined', ref: 'party' })
  })

  it('换一个房间进入时立刻离开当前房间，不等新房间查到', async () => {
    const { roomEntryStore, roomStore } = await entered()
    routes.set('GET /api/rooms/b', found(listing({ id: 'rb' })))
    const entering = roomEntryStore.enter('b')
    expect(socketAt(0).readyState).toBe(FakeSocket.CLOSED)
    expect(roomStore.state.room).toBeNull()
    await entering
  })
})

describe('查不到房间', () => {
  it('恢复上次的房间时它已经不存在，进入 not-found，不弹提示，并忘掉这个房间', async () => {
    local.setItem(lastRoomKey, 'r1')
    routes.set('GET /api/rooms/r1', notFound)
    const { roomEntryStore, notifications } = await load()
    expect(roomEntryStore.resume()).toBe('r1')
    await flush()
    expect(roomEntryStore.state).toEqual({ phase: 'not-found', ref: 'r1', room: null })
    expect(notifications()).toEqual([])
    expect(local.getItem(lastRoomKey)).toBeNull()
  })

  it('不存在的是另一个房间时，上次进过的房间还记着', async () => {
    local.setItem(lastRoomKey, 'r1')
    routes.set('GET /api/rooms/gone', notFound)
    const { roomEntryStore } = await load()
    await roomEntryStore.enter('gone')
    expect(roomEntryStore.state.phase).toBe('not-found')
    expect(local.getItem(lastRoomKey)).toBe('r1')
  })

  it('因为别的原因查询失败时提示服务端给的原因，进入 failed，重试时再查一次', async () => {
    routes.set('GET /api/rooms/r1', unavailable)
    const { roomEntryStore, notifications } = await load()
    await roomEntryStore.enter('r1')
    expect(roomEntryStore.state).toEqual({ phase: 'failed', ref: 'r1', room: null })
    expect(notifications()).toEqual([{ tone: 'error', message: '服务暂时不可用，请稍后再试。' }])

    routes.set('GET /api/rooms/r1', found(listing()))
    roomEntryStore.retry()
    await flush()
    expect(roomEntryStore.state.phase).toBe('needs-ticket')
  })
})

describe('被新的尝试取代', () => {
  it('换了要进的房间之后，先发出的查询晚到也不生效', async () => {
    local.setItem(ticketKey, 't1')
    const late = deferred<Response>()
    routes.set('GET /api/rooms/r1', () => late.promise)
    routes.set('GET /api/rooms/b', found(listing({ id: 'rb' })))
    const { roomEntryStore } = await load()
    const first = roomEntryStore.enter('r1')
    await roomEntryStore.enter('b')

    late.resolve(json(200, listing()))
    await first
    expect(roomEntryStore.state).toEqual({ phase: 'needs-ticket', ref: 'b', room: listing({ id: 'rb' }) })
    expect(sockets).toHaveLength(0)
  })

  it('换了要进的房间之后，先发出的查询晚失败也不提示、不改状态', async () => {
    const late = deferred<Response>()
    routes.set('GET /api/rooms/r1', () => late.promise)
    routes.set('GET /api/rooms/b', found(listing({ id: 'rb' })))
    const { roomEntryStore, notifications } = await load()
    const first = roomEntryStore.enter('r1')
    await roomEntryStore.enter('b')

    late.resolve(unavailable())
    await first
    expect(roomEntryStore.state).toEqual({ phase: 'needs-ticket', ref: 'b', room: listing({ id: 'rb' }) })
    expect(notifications()).toEqual([])
  })

  it('取消进入之后，还在路上的查询结果不生效', async () => {
    local.setItem(ticketKey, 't1')
    const late = deferred<Response>()
    routes.set('GET /api/rooms/r1', () => late.promise)
    const { roomEntryStore } = await load()
    const first = roomEntryStore.enter('r1')
    roomEntryStore.reset()

    late.resolve(json(200, listing()))
    await first
    expect(roomEntryStore.state).toEqual({ phase: 'idle', ref: null, room: null })
    expect(sockets).toHaveLength(0)
  })
})

describe('连接被服务端关闭', () => {
  it('票据被拒绝时删掉它，重新查询房间，等用户再换票据', async () => {
    local.setItem(ticketKey, 't1')
    const { roomEntryStore } = await load()
    await roomEntryStore.enter('r1')

    // 房间在这之后加了密码，所以重新查到的房间信息和进房时的不一样。
    routes.set('GET /api/rooms/r1', found(listing({ hasPassword: true })))
    socketAt(0).open()
    socketAt(0).lose(CloseCode.unauthorized)
    await flush()
    expect(local.getItem(ticketKey)).toBeNull()
    expect(roomEntryStore.state).toEqual({ phase: 'needs-ticket', ref: 'r1', room: listing({ hasPassword: true }) })
    expect(sockets).toHaveLength(1)
  })

  it('进入之后房间改了密码，票据作废，同样重新查询房间，等用户再换票据', async () => {
    const { roomEntryStore } = await entered()
    routes.set('GET /api/rooms/r1', found(listing({ hasPassword: true })))
    socketAt(0).lose(CloseCode.unauthorized)
    await flush()
    expect(local.getItem(ticketKey)).toBeNull()
    expect(roomEntryStore.state).toEqual({ phase: 'needs-ticket', ref: 'r1', room: listing({ hasPassword: true }) })
  })

  it('进入之后房间被关闭，进入 not-found 并忘掉这个房间，用别名进的也一样', async () => {
    routes.set('GET /api/rooms/party', found(listing({ slug: 'party' })))
    const { roomEntryStore, roomStore } = await entered('party')
    expect(local.getItem(lastRoomKey)).toBe('r1')

    socketAt(0).lose(CloseCode.notFound)
    expect(roomEntryStore.state.phase).toBe('not-found')
    expect(roomStore.state).toMatchObject({ roomId: null, stopCode: null })
    expect(local.getItem(lastRoomKey)).toBeNull()
  })

  it('握手因为参数无效被拒绝时进入 failed', async () => {
    local.setItem(ticketKey, 't1')
    const { roomEntryStore, roomStore } = await load()
    await roomEntryStore.enter('r1')
    socketAt(0).open()
    socketAt(0).lose(CloseCode.invalidArgument)
    expect(roomEntryStore.state.phase).toBe('failed')
    expect(roomStore.state).toMatchObject({ roomId: null, stopCode: null })
  })
})

describe('在加入弹窗里确认', () => {
  const route = 'POST /api/rooms/r1/tickets'
  const locked = listing({ hasPassword: true })

  beforeEach(() => {
    local.setItem(memberTokenKey, 'good')
  })

  it('没有票据时带着密码换取票据，保存之后用它进入', async () => {
    routes.set(route, () => json(201, { ticket: 't9' }))
    const { roomEntryStore } = await load()
    await expect(roomEntryStore.join(locked, '芝麻开门')).resolves.toBe(true)
    expect(bodies.get(route)).toEqual({ password: '芝麻开门' })
    expect(local.getItem(ticketKey)).toBe('t9')
    expect(roomEntryStore.state).toEqual({ phase: 'joining', ref: 'r1', room: locked })
    socketAt(0).open()
    expect(socketAt(0).last('hello').data).toMatchObject({ roomId: 'r1', ticket: 't9' })
  })

  it('房间没有密码时换票据不带密码，有别名的房间按别名进入', async () => {
    routes.set(route, () => json(201, { ticket: 't9' }))
    const { roomEntryStore } = await load()
    await expect(roomEntryStore.join(listing({ slug: 'party' }), '')).resolves.toBe(true)
    expect(bodies.get(route)).toEqual({})
    expect(roomEntryStore.state).toMatchObject({ phase: 'joining', ref: 'party' })
  })

  it('已经有票据时不再换取', async () => {
    local.setItem(ticketKey, 't1')
    const { roomEntryStore } = await load()
    await expect(roomEntryStore.join(listing(), '')).resolves.toBe(true)
    expect(requests).toEqual([])
    expect(roomEntryStore.state.phase).toBe('joining')
  })

  it('换票据失败时提示原因，不进入', async () => {
    routes.set(route, () => errorResponse(429, 'RATE_LIMITED', '这个房间的密码尝试次数太多，请稍后再试。'))
    const { roomEntryStore, notifications } = await load()
    await expect(roomEntryStore.join(locked, '猜的')).resolves.toBe(false)
    expect(notifications()).toEqual([{ tone: 'error', message: '这个房间的密码尝试次数太多，请稍后再试。' }])
    expect(roomEntryStore.state.phase).toBe('idle')
    expect(sockets).toHaveLength(0)
  })

  it('已经在这个房间里时不重连', async () => {
    const { roomEntryStore } = await entered()
    await expect(roomEntryStore.join(listing(), '')).resolves.toBe(true)
    expect(sockets).toHaveLength(1)
    expect(socketAt(0).readyState).toBe(FakeSocket.OPEN)
    expect(roomEntryStore.state.phase).toBe('joined')
  })

  it('换票据期间关掉弹窗，票据换到之后留着，但不进入', async () => {
    const ticket = deferred<Response>()
    routes.set(route, () => ticket.promise)
    const { roomEntryStore } = await load()
    const joining = roomEntryStore.join(locked, '芝麻开门')
    await flush()
    roomEntryStore.cancelJoin()
    ticket.resolve(json(201, { ticket: 't9' }))
    await expect(joining).resolves.toBe(false)
    expect(local.getItem(ticketKey)).toBe('t9')
    expect(roomEntryStore.state.phase).toBe('idle')
    expect(sockets).toHaveLength(0)
  })

  it('换票据期间关掉弹窗，之后换票据失败也不提示', async () => {
    const ticket = deferred<Response>()
    routes.set(route, () => ticket.promise)
    const { roomEntryStore, notifications } = await load()
    const joining = roomEntryStore.join(locked, '猜的')
    await flush()
    roomEntryStore.cancelJoin()
    ticket.resolve(errorResponse(429, 'RATE_LIMITED', '这个房间的密码尝试次数太多，请稍后再试。'))
    await expect(joining).resolves.toBe(false)
    expect(notifications()).toEqual([])
  })

  it('换票据期间进了别的房间，票据换到之后不把人带回来', async () => {
    const ticket = deferred<Response>()
    routes.set(route, () => ticket.promise)
    routes.set('GET /api/rooms/b', found(listing({ id: 'rb' })))
    local.setItem('alisten.ticket.rb', 'tb')
    const { roomEntryStore } = await load()
    const joining = roomEntryStore.join(locked, '芝麻开门')
    await flush()
    await roomEntryStore.enter('b')
    ticket.resolve(json(201, { ticket: 't9' }))
    await expect(joining).resolves.toBe(false)
    expect(roomEntryStore.state).toMatchObject({ phase: 'joining', ref: 'b' })
    expect(sockets).toHaveLength(1)
  })

  it('等票据的进房被取消之后，还在换的票据不再生效', async () => {
    const ticket = deferred<Response>()
    routes.set(route, () => ticket.promise)
    routes.set('GET /api/rooms/r1', found(locked))
    const { roomEntryStore } = await load()
    await roomEntryStore.enter('r1')
    expect(roomEntryStore.state.phase).toBe('needs-ticket')
    const joining = roomEntryStore.join(locked, '芝麻开门')
    await flush()
    roomEntryStore.reset()
    ticket.resolve(json(201, { ticket: 't9' }))
    await expect(joining).resolves.toBe(false)
    expect(roomEntryStore.state).toEqual({ phase: 'idle', ref: null, room: null })
    expect(sockets).toHaveLength(0)
  })

  it('还在查询的另一个房间不受弹窗影响', async () => {
    const lookup = deferred<Response>()
    routes.set('GET /api/rooms/b', () => lookup.promise)
    routes.set(route, () => errorResponse(429, 'RATE_LIMITED', '这个房间的密码尝试次数太多，请稍后再试。'))
    local.setItem('alisten.ticket.rb', 'tb')
    const { roomEntryStore } = await load()
    const entering = roomEntryStore.enter('b')
    await expect(roomEntryStore.join(locked, '猜的')).resolves.toBe(false)
    roomEntryStore.cancelJoin()
    lookup.resolve(json(200, listing({ id: 'rb' })))
    await entering
    expect(roomEntryStore.state).toMatchObject({ phase: 'joining', ref: 'b' })
  })
})

describe('换取票据失败时的提示', () => {
  const route = 'POST /api/rooms/r1/tickets'

  it('被限流时原样显示服务端给用户看的消息', async () => {
    local.setItem(memberTokenKey, 'good')
    routes.set(route, () => errorResponse(429, 'RATE_LIMITED', '这个房间的密码尝试次数太多，请稍后再试。'))
    const { api, joinErrorMessage } = await load()
    const error: unknown = await api.createTicket('r1', { password: '芝麻开门' }).catch((reason: unknown) => reason)
    expect(joinErrorMessage(error)).toBe('这个房间的密码尝试次数太多，请稍后再试。')
  })

  it('身份被换成新的游客时，说明这次没有加入房间', async () => {
    local.setItem(memberTokenKey, 'expired')
    const unauthorized = () => errorResponse(401, 'UNAUTHORIZED', '身份已失效')
    routes.set(route, unauthorized)
    routes.set('GET /api/me', token => token === 'issued' ? json(200, { member }) : unauthorized())
    routes.set('POST /api/members', () => json(201, { member, memberToken: 'issued' }))
    const { api, joinErrorMessage } = await load()
    const error: unknown = await api.createTicket('r1', {}).catch((reason: unknown) => reason)
    expect(joinErrorMessage(error)).toBe('没有加入房间，再加入一次。')
  })
})

describe('复制邀请链接', () => {
  it('链接指向房间的别名，没有别名时指向 id', async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>(() => Promise.resolve())
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const { copyInviteLink, notifications } = await load()
    await copyInviteLink({ id: 'r1', slug: 'party' })
    await copyInviteLink({ id: 'r1', slug: null })
    expect(writeText.mock.calls).toEqual([['https://alisten.test/r/party'], ['https://alisten.test/r/r1']])
    expect(notifications().map(({ tone }) => tone)).toEqual(['success', 'success'])
  })

  it.each([
    ['非安全上下文没有剪贴板接口', {}],
    ['用户拒绝授权', { clipboard: { writeText: () => Promise.reject(new Error('NotAllowedError')) } }],
  ])('%s时，把链接放进提示里让用户手动复制', async (_, navigator) => {
    vi.stubGlobal('navigator', navigator)
    const { copyInviteLink, notifications } = await load()
    await copyInviteLink({ id: 'r1', slug: null })
    expect(notifications()).toEqual([{ tone: 'info', message: expect.stringContaining('https://alisten.test/r/r1') as unknown }])
  })
})
