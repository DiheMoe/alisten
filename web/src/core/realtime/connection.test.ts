import type { HelloResponse, RoomEventMessage } from './protocol.gen'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clock } from '@/core/clock'
import { Connection, RequestError } from './connection'
import { CloseCode, PROTOCOL_VERSION } from './protocol.gen'

interface Sent {
  type: string
  id: string
  data: Record<string, unknown>
}

const abnormalClosure = 1006
const helloResponse = { room: { term: 1, seq: 0 } } as unknown as HelloResponse
const roomEvent = { kind: 'event', type: 'queue.updated', term: 1, seq: 1, data: { pending: null, items: [] } } as unknown as RoomEventMessage

let sockets: FakeSocket[]
let page: EventTarget & { visibilityState: string }
let browser: EventTarget

class FakeSocket extends EventTarget {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 3
  readyState = FakeSocket.CONNECTING
  readonly sent: Sent[] = []

  constructor(readonly url: string) {
    super()
    sockets.push(this)
  }

  send(message: string): void {
    this.sent.push(JSON.parse(message) as Sent)
  }

  // 和浏览器一样，close 事件要到之后的任务才派发，这里不派发：测试用 lose() 模拟它。
  close(): void {
    this.readyState = FakeSocket.CLOSED
  }

  open(): void {
    this.readyState = FakeSocket.OPEN
    this.dispatchEvent(new Event('open'))
  }

  receiveRaw(data: string): void {
    this.dispatchEvent(Object.assign(new Event('message'), { data }))
  }

  receive(message: unknown): void {
    this.receiveRaw(JSON.stringify(message))
  }

  respond(request: Sent, data: unknown = {}): void {
    this.receive({ kind: 'response', type: request.type, id: request.id, data })
  }

  reject(request: Sent, code: string, message: string): void {
    this.receive({ kind: 'response', type: request.type, id: request.id, data: {}, error: { code, message } })
  }

  lose(code: number): void {
    this.readyState = FakeSocket.CLOSED
    this.dispatchEvent(Object.assign(new Event('close'), { code }))
  }

  last(type: string): Sent {
    const message = this.sent.filter(sent => sent.type === type).at(-1)
    if (!message)
      throw new Error(`没有发出 ${type}`)
    return message
  }

  count(type: string): number {
    return this.sent.filter(sent => sent.type === type).length
  }
}

function socketAt(index: number): FakeSocket {
  const socket = sockets[index]
  if (!socket)
    throw new Error(`没有第 ${index} 个连接`)
  return socket
}

function start() {
  const handlers = { status: vi.fn(), hello: vi.fn(), event: vi.fn(), error: vi.fn(), stop: vi.fn() }
  const connection = new Connection('r1', 't1', handlers)
  connection.start()
  return { connection, handlers }
}

const flush = () => vi.advanceTimersByTimeAsync(0)

async function handshake(socket: FakeSocket): Promise<void> {
  socket.open()
  socket.respond(socket.last('hello'), helloResponse)
  await flush()
}

async function started() {
  const result = start()
  await handshake(socketAt(0))
  return result
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(Math, 'random').mockReturnValue(0.5)
  sockets = []
  page = Object.assign(new EventTarget(), { visibilityState: 'visible' })
  browser = Object.assign(new EventTarget(), { location: { protocol: 'https:', host: 'alisten.test' } })
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.stubGlobal('document', page)
  vi.stubGlobal('window', browser)
  clock.reset()
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('握手', () => {
  it('连上之后先握手，握手成功才算打开', async () => {
    const { handlers } = start()
    const socket = socketAt(0)
    expect(socket.url).toBe('wss://alisten.test/ws')
    socket.open()
    expect(socket.last('hello').data).toEqual({ roomId: 'r1', ticket: 't1', protocolVersion: PROTOCOL_VERSION })
    expect(handlers.status).not.toHaveBeenCalled()

    socket.respond(socket.last('hello'), helloResponse)
    await flush()
    expect(handlers.status).toHaveBeenCalledWith('open')
    expect(handlers.hello).toHaveBeenCalledWith(helloResponse)
  })

  it('握手响应之前的房间事件已经包含在快照里，不交给处理方', async () => {
    const { handlers } = start()
    const socket = socketAt(0)
    socket.open()
    socket.receive(roomEvent)
    expect(handlers.event).not.toHaveBeenCalled()

    socket.respond(socket.last('hello'), helloResponse)
    await flush()
    socket.receive(roomEvent)
    expect(handlers.event).toHaveBeenCalledTimes(1)
  })

  it('服务端一直不响应时，30 秒后放弃这条连接并重连', async () => {
    const { handlers } = start()
    const socket = socketAt(0)
    socket.open()
    await vi.advanceTimersByTimeAsync(29_999)
    expect(handlers.status).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1)
    expect(handlers.status).toHaveBeenCalledWith('reconnecting')
    expect(socket.readyState).toBe(FakeSocket.CLOSED)
  })

  it('握手被拒绝时只报告错误，是否重连由随后的关闭码决定', async () => {
    const { handlers } = start()
    const socket = socketAt(0)
    socket.open()
    socket.reject(socket.last('hello'), 'UNAUTHORIZED', '票据无效')
    await flush()
    expect(handlers.error).toHaveBeenCalledWith({ code: 'UNAUTHORIZED', message: '票据无效' })
    expect(handlers.status).not.toHaveBeenCalled()

    socket.lose(CloseCode.unauthorized)
    expect(handlers.stop).toHaveBeenCalledWith(CloseCode.unauthorized)
  })
})

describe('断线之后', () => {
  it.each([
    ['参数无效', CloseCode.invalidArgument],
    ['未授权', CloseCode.unauthorized],
    ['房间不存在', CloseCode.notFound],
    ['客户端版本过旧', CloseCode.clientOutdated],
  ])('%s时停止，不再重连', async (_, code) => {
    const { handlers } = await started()
    socketAt(0).lose(code)
    expect(handlers.stop).toHaveBeenCalledWith(code)
    expect(handlers.status).toHaveBeenLastCalledWith('stopped')

    await vi.advanceTimersByTimeAsync(120_000)
    expect(sockets).toHaveLength(1)
  })

  it.each([
    ['连接异常断开', abnormalClosure],
    ['服务端内部错误', CloseCode.internal],
    ['服务重启', CloseCode.serviceRestart],
    ['服务暂时不可用', CloseCode.unavailable],
    ['握手超时', CloseCode.handshakeTimeout],
    ['发送队列已满', CloseCode.sendQueueFull],
  ])('%s时重连', async (_, code) => {
    const { handlers } = await started()
    socketAt(0).lose(code)
    expect(handlers.stop).not.toHaveBeenCalled()
    expect(handlers.status).toHaveBeenLastCalledWith('reconnecting')

    await vi.advanceTimersByTimeAsync(3000)
    expect(sockets).toHaveLength(2)
  })

  it('连续失败时等待时间加倍，握手成功之后重新计算', async () => {
    await started()
    socketAt(0).lose(abnormalClosure)
    await vi.advanceTimersByTimeAsync(2999)
    expect(sockets).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(sockets).toHaveLength(2)

    socketAt(1).lose(abnormalClosure)
    await vi.advanceTimersByTimeAsync(5999)
    expect(sockets).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(sockets).toHaveLength(3)

    await handshake(socketAt(2))
    socketAt(2).lose(abnormalClosure)
    await vi.advanceTimersByTimeAsync(3000)
    expect(sockets).toHaveLength(4)
  })

  it('error 之后紧跟的 close 只算一次断线', async () => {
    await started()
    const socket = socketAt(0)
    socket.dispatchEvent(new Event('error'))
    socket.lose(abnormalClosure)
    await vi.advanceTimersByTimeAsync(3000)
    expect(sockets).toHaveLength(2)
  })

  it('已经换掉的连接上的事件不再起作用', async () => {
    const { handlers } = await started()
    const old = socketAt(0)
    old.lose(abnormalClosure)
    await vi.advanceTimersByTimeAsync(3000)
    await handshake(socketAt(1))
    handlers.status.mockClear()
    handlers.event.mockClear()

    old.receive(roomEvent)
    old.lose(CloseCode.unauthorized)
    expect(handlers.event).not.toHaveBeenCalled()
    expect(handlers.stop).not.toHaveBeenCalled()
    expect(handlers.status).not.toHaveBeenCalled()
  })
})

describe('半开连接', () => {
  it('握手之后每 5 秒 ping 一次，时间取整，回包用来校准时钟', async () => {
    await started()
    const socket = socketAt(0)
    expect(socket.count('ping')).toBe(1)
    const ping = socket.last('ping')
    expect(Number.isInteger(ping.data.clientTimeMs)).toBe(true)
    expect(clock.offsetMs).toBeNull()

    socket.respond(ping, { serverTimeMs: 1000 })
    await flush()
    expect(clock.offsetMs).not.toBeNull()

    await vi.advanceTimersByTimeAsync(5000)
    expect(socket.count('ping')).toBe(2)
  })

  it('ping 一直收到回包时保持连接', async () => {
    const { handlers } = await started()
    const socket = socketAt(0)
    socket.respond(socket.last('ping'), { serverTimeMs: 1000 })
    for (let i = 0; i < 6; i++) {
      await vi.advanceTimersByTimeAsync(5000)
      socket.respond(socket.last('ping'), { serverTimeMs: 1000 })
    }
    expect(handlers.status).toHaveBeenCalledTimes(1)
    expect(sockets).toHaveLength(1)
  })

  it('ping 15 秒收不到回包就当作断线，只重连一次', async () => {
    const { handlers } = await started()
    const socket = socketAt(0)
    await vi.advanceTimersByTimeAsync(14_999)
    expect(handlers.status).toHaveBeenLastCalledWith('open')

    await vi.advanceTimersByTimeAsync(1)
    expect(handlers.status).toHaveBeenLastCalledWith('reconnecting')
    expect(socket.readyState).toBe(FakeSocket.CLOSED)

    await vi.advanceTimersByTimeAsync(3000)
    expect(sockets).toHaveLength(2)
  })
})

describe('请求', () => {
  it('没有连上时直接失败，不发出', async () => {
    const { connection } = start()
    await expect(connection.request('room.sync', {})).rejects.toThrow('还没有连上房间')
    socketAt(0).open()
    await expect(connection.request('room.sync', {})).rejects.toThrow('还没有连上房间')
    expect(socketAt(0).count('room.sync')).toBe(0)
  })

  it('按 id 匹配响应', async () => {
    const { connection } = await started()
    const socket = socketAt(0)
    const first = connection.request('room.sync', {})
    const firstRequest = socket.last('room.sync')
    const second = connection.request('room.sync', {})
    socket.respond(socket.last('room.sync'), { room: 'second' })
    socket.respond(firstRequest, { room: 'first' })
    await expect(first).resolves.toEqual({ room: 'first' })
    await expect(second).resolves.toEqual({ room: 'second' })
  })

  it('服务端返回错误时带上错误码', async () => {
    const { connection } = await started()
    const socket = socketAt(0)
    const pending = connection.request('room.sync', {})
    socket.reject(socket.last('room.sync'), 'NOT_FOUND', '房间不存在')
    await expect(pending).rejects.toEqual(new RequestError('房间不存在', 'NOT_FOUND'))
    await expect(pending).rejects.toHaveProperty('code', 'NOT_FOUND')
  })

  it('60 秒没有响应就失败，之后才到的响应被忽略，连接不受影响', async () => {
    const { connection, handlers } = await started()
    const socket = socketAt(0)
    const pending = connection.request('room.sync', {})
    const rejected = expect(pending).rejects.toHaveProperty('code', null)
    const pongs = setInterval(() => socket.respond(socket.last('ping'), { serverTimeMs: 1000 }), 1000)
    await vi.advanceTimersByTimeAsync(60_000)
    clearInterval(pongs)
    await rejected
    await expect(pending).rejects.toThrow('服务器没有及时响应')

    socket.respond(socket.sent.find(sent => sent.type === 'room.sync') as Sent, {})
    expect(handlers.status).toHaveBeenLastCalledWith('open')
  })

  it('断线时拒绝所有等待中的请求，不让它们悬挂到超时', async () => {
    const { connection } = await started()
    const pending = [connection.request('room.sync', {}), connection.request('room.sync', {})]
    socketAt(0).lose(abnormalClosure)
    for (const request of pending) {
      await expect(request).rejects.toThrow('与房间的连接断开了')
      await expect(request).rejects.toHaveProperty('code', null)
    }
    expect(vi.getTimerCount()).toBe(1)
  })

  it('格式不对的消息被忽略，不影响之后的消息', async () => {
    const { handlers } = await started()
    const socket = socketAt(0)
    socket.receiveRaw('不是 JSON')
    socket.receive(roomEvent)
    expect(handlers.event).toHaveBeenCalledTimes(1)
  })

  it('连接级的错误事件交给处理方', async () => {
    const { handlers } = await started()
    socketAt(0).receive({ kind: 'event', type: 'error', data: { code: 'INTERNAL', message: '出错了' } })
    expect(handlers.error).toHaveBeenCalledWith({ code: 'INTERNAL', message: '出错了' })
    expect(handlers.event).not.toHaveBeenCalled()
  })
})

describe('网络和页面状态变化', () => {
  it('网络恢复时取消等待、立刻重连，并重新计算等待时间', async () => {
    await started()
    socketAt(0).lose(abnormalClosure)
    await vi.advanceTimersByTimeAsync(3000)
    socketAt(1).lose(abnormalClosure)

    browser.dispatchEvent(new Event('online'))
    expect(sockets).toHaveLength(3)
    await vi.advanceTimersByTimeAsync(6000)
    expect(sockets).toHaveLength(3)

    socketAt(2).lose(abnormalClosure)
    await vi.advanceTimersByTimeAsync(3000)
    expect(sockets).toHaveLength(4)
  })

  it('网络恢复时连接还开着就发一次 ping 探测', async () => {
    await started()
    const socket = socketAt(0)
    browser.dispatchEvent(new Event('online'))
    expect(socket.count('ping')).toBe(2)
    expect(sockets).toHaveLength(1)
  })

  it('正在建立连接时不重复建立', () => {
    start()
    browser.dispatchEvent(new Event('online'))
    expect(sockets).toHaveLength(1)
  })

  it('回到前台时清空时钟样本并探测连接，切到后台时什么也不做', async () => {
    await started()
    const socket = socketAt(0)
    socket.respond(socket.last('ping'), { serverTimeMs: 1000 })
    await flush()
    expect(clock.offsetMs).not.toBeNull()

    page.visibilityState = 'hidden'
    page.dispatchEvent(new Event('visibilitychange'))
    expect(clock.offsetMs).not.toBeNull()
    expect(socket.count('ping')).toBe(1)

    page.visibilityState = 'visible'
    page.dispatchEvent(new Event('visibilitychange'))
    expect(clock.offsetMs).toBeNull()
    expect(socket.count('ping')).toBe(2)
  })

  it('页面从往返缓存恢复时才探测连接', async () => {
    await started()
    const socket = socketAt(0)
    browser.dispatchEvent(Object.assign(new Event('pageshow'), { persisted: false }))
    expect(socket.count('ping')).toBe(1)
    browser.dispatchEvent(Object.assign(new Event('pageshow'), { persisted: true }))
    expect(socket.count('ping')).toBe(2)
  })

  it('停止之后关闭连接，不再响应网络和页面的变化', async () => {
    const { connection, handlers } = await started()
    const socket = socketAt(0)
    connection.stop()
    expect(socket.readyState).toBe(FakeSocket.CLOSED)
    expect(handlers.status).toHaveBeenLastCalledWith('stopped')

    browser.dispatchEvent(new Event('online'))
    page.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(120_000)
    expect(sockets).toHaveLength(1)
    expect(socket.count('ping')).toBe(1)
  })

  it('等待重连时停止，到时间也不再连接', async () => {
    const { connection } = await started()
    socketAt(0).lose(abnormalClosure)
    connection.stop()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(sockets).toHaveLength(1)
  })
})
