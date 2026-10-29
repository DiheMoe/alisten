import type { ErrorCode, ErrorInfo, HelloResponse, RequestDataMap, RequestMessage, RequestType, ResponseDataMap, RoomEventMessage, ServerMessage } from './protocol.gen'
import { clock } from '@/core/clock'
import { reconnectDelay } from './backoff'
import { CloseCode, PROTOCOL_VERSION } from './protocol.gen'

const requestTimeoutMs = 60_000
const pingIntervalMs = 5000

const stopCodes = new Set<number>([CloseCode.invalidArgument, CloseCode.unauthorized, CloseCode.notFound, CloseCode.clientOutdated])

// 浏览器在连接失败时先触发 error，再以 1006 触发 close。
const abnormalClosure = 1006

export type ConnectionStatus = 'connecting' | 'open' | 'reconnecting' | 'stopped'

export class RequestError extends Error {
  readonly code: ErrorCode | null

  constructor(message: string, code: ErrorCode | null) {
    super(message)
    this.name = 'RequestError'
    this.code = code
  }
}

export interface ConnectionHandlers {
  status: (status: ConnectionStatus) => void
  hello: (response: HelloResponse) => void
  event: (event: RoomEventMessage) => void
  error: (error: ErrorInfo) => void
  stop: (code: number) => void
}

interface Pending {
  resolve: (data: unknown) => void
  reject: (error: RequestError) => void
  timer: ReturnType<typeof setTimeout>
}

function socketUrl(): string {
  return `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`
}

export class Connection {
  readonly #roomId: string
  readonly #ticket: string
  readonly #handlers: ConnectionHandlers
  #status: ConnectionStatus = 'connecting'
  #socket: WebSocket | null = null
  #ready = false
  #attempt = 0
  #nextId = 0
  #pending = new Map<string, Pending>()
  #retryTimer: ReturnType<typeof setTimeout> | undefined
  #pingTimer: ReturnType<typeof setInterval> | undefined

  constructor(roomId: string, ticket: string, handlers: ConnectionHandlers) {
    this.#roomId = roomId
    this.#ticket = ticket
    this.#handlers = handlers
  }

  start(): void {
    document.addEventListener('visibilitychange', this.#onVisibilityChange)
    window.addEventListener('online', this.check)
    window.addEventListener('pageshow', this.#onPageShow)
    this.#open()
  }

  stop(): void {
    this.#halt()
  }

  request<T extends RequestType>(type: T, data: RequestDataMap[T]): Promise<ResponseDataMap[T]> {
    if (!this.#ready)
      return Promise.reject(new RequestError('还没有连上房间，连上之后再试一次。', null))
    return this.#send(type, data)
  }

  // 不以收不到回包作为断线的依据：连接已失效时，写入 ping 会触发 close 或 error。
  readonly check = (): void => {
    if (this.#status === 'stopped')
      return
    const socket = this.#socket
    if (socket?.readyState === WebSocket.OPEN) {
      if (this.#ready)
        this.#ping()
      return
    }
    if (socket?.readyState === WebSocket.CONNECTING)
      return
    this.#detach()
    clearTimeout(this.#retryTimer)
    this.#attempt = 0
    this.#open()
  }

  #onVisibilityChange = (): void => {
    if (document.visibilityState !== 'visible')
      return
    clock.reset()
    this.check()
  }

  #onPageShow = (event: PageTransitionEvent): void => {
    if (event.persisted)
      this.check()
  }

  #open(): void {
    const socket = new WebSocket(socketUrl())
    this.#socket = socket
    socket.addEventListener('open', () => {
      if (socket === this.#socket)
        this.#hello(socket)
    })
    socket.addEventListener('message', (event) => {
      if (socket === this.#socket)
        this.#receive(event)
    })
    socket.addEventListener('close', event => this.#lost(socket, event.code))
    socket.addEventListener('error', () => this.#lost(socket, abnormalClosure))
  }

  #hello(socket: WebSocket): void {
    this.#send('hello', { roomId: this.#roomId, ticket: this.#ticket, protocolVersion: PROTOCOL_VERSION }).then((response) => {
      if (socket !== this.#socket)
        return
      this.#ready = true
      this.#attempt = 0
      clock.reset()
      this.#setStatus('open')
      this.#handlers.hello(response)
      this.#ping()
      this.#pingTimer = setInterval(() => this.#ping(), pingIntervalMs)
    }, (error: RequestError) => {
      if (socket !== this.#socket)
        return
      // 服务端返回错误后会以对应的关闭码断开，是否重连由关闭码决定。
      if (error.code)
        this.#handlers.error({ code: error.code, message: error.message })
      else
        this.#lost(socket, abnormalClosure)
    })
  }

  #ping(): void {
    const sentAt = clock.now()
    // 服务端只接受整数毫秒，而 performance.now() 带小数。
    this.#send('ping', { clientTimeMs: Math.round(sentAt) }).then((response) => {
      clock.addSample(sentAt, clock.now(), response.serverTimeMs)
    }, () => {})
  }

  #send<T extends RequestType>(type: T, data: RequestDataMap[T]): Promise<ResponseDataMap[T]> {
    const socket = this.#socket
    if (!socket)
      return Promise.reject(new RequestError('还没有连上房间，连上之后再试一次。', null))
    const id = String(++this.#nextId)
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id)
        reject(new RequestError('服务器没有及时响应，请稍后再试。', null))
      }, requestTimeoutMs)
      this.#pending.set(id, { resolve: resolve as (data: unknown) => void, reject, timer })
      const message: RequestMessage<T> = { kind: 'request', type, id, data }
      socket.send(JSON.stringify(message))
    })
  }

  #receive(event: MessageEvent): void {
    let message: ServerMessage
    try {
      message = JSON.parse(event.data as string) as ServerMessage
    }
    catch {
      return
    }
    if (message.kind === 'response') {
      const pending = this.#pending.get(message.id)
      if (!pending)
        return
      this.#pending.delete(message.id)
      clearTimeout(pending.timer)
      if (message.error)
        pending.reject(new RequestError(message.error.message, message.error.code))
      else
        pending.resolve(message.data)
      return
    }
    if (message.type === 'error')
      this.#handlers.error(message.data)
    // 握手响应里的快照已经包含握手之前的事件。
    else if (this.#ready)
      this.#handlers.event(message)
  }

  #lost(socket: WebSocket, code: number): void {
    if (socket !== this.#socket)
      return
    this.#detach()
    if (stopCodes.has(code)) {
      this.#halt()
      this.#handlers.stop(code)
      return
    }
    this.#attempt += 1
    this.#setStatus('reconnecting')
    this.#retryTimer = setTimeout(() => this.#open(), reconnectDelay(this.#attempt))
  }

  #detach(): void {
    const socket = this.#socket
    if (!socket)
      return
    this.#socket = null
    this.#ready = false
    clearInterval(this.#pingTimer)
    if (socket.readyState === WebSocket.CONNECTING || socket.readyState === WebSocket.OPEN)
      socket.close()
    for (const pending of this.#pending.values()) {
      clearTimeout(pending.timer)
      pending.reject(new RequestError('与房间的连接断开了，连上之后再试一次。', null))
    }
    this.#pending.clear()
  }

  #setStatus(status: ConnectionStatus): void {
    if (status === this.#status)
      return
    this.#status = status
    this.#handlers.status(status)
  }

  #halt(): void {
    this.#detach()
    clearTimeout(this.#retryTimer)
    document.removeEventListener('visibilitychange', this.#onVisibilityChange)
    window.removeEventListener('online', this.check)
    window.removeEventListener('pageshow', this.#onPageShow)
    this.#setStatus('stopped')
  }
}
