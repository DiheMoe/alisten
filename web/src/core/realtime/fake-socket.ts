import { vi } from 'vitest'

// 测试里代替浏览器 WebSocket 的假实现，只被测试引用。

export interface Sent {
  type: string
  id: string
  data: Record<string, unknown>
}

// 按建立的先后保存，stubBrowser 会清空。
export const sockets: FakeSocket[] = []

export class FakeSocket extends EventTarget {
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

  // 浏览器要到之后的任务才派发 close 事件，所以 close() 不派发。测试用 lose() 模拟这个 close 事件。
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

export function socketAt(index: number): FakeSocket {
  const socket = sockets[index]
  if (!socket)
    throw new Error(`没有第 ${index} 个连接`)
  return socket
}

// 换上假的 WebSocket、document 和 window。返回的 page 和 browser 用来派发页面和网络状态的事件。
export function stubBrowser(): { page: EventTarget & { visibilityState: string }, browser: EventTarget } {
  sockets.length = 0
  const page = Object.assign(new EventTarget(), { visibilityState: 'visible' })
  const browser = Object.assign(new EventTarget(), { location: { protocol: 'https:', host: 'alisten.test', origin: 'https://alisten.test' } })
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.stubGlobal('document', page)
  vi.stubGlobal('window', browser)
  return { page, browser }
}
