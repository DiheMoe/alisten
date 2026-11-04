import type { IdentityEvent } from './client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const memberTokenKey = 'alisten.member-token'
const adminTokenKey = 'alisten.admin-token'
const member = { id: 'm1', name: 'alice', avatarHash: '' }
const issued = { id: 'm2', name: 'bob', avatarHash: '' }

type Handler = (token: string | null) => Response | Promise<Response>

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

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status })
}

function errorResponse(status: number, code: string, message: string): Response {
  return json(status, { error: { code, message } })
}

const unauthorized = () => errorResponse(401, 'UNAUTHORIZED', '身份已失效')
const wrongPassword = () => errorResponse(401, 'UNAUTHORIZED', '房间密码错误')

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

let routes: Map<string, Handler>
let calls: { route: string, token: string | null }[]
let unexpected: string[]
let validTokens: Set<string>
let local: FakeStorage
let session: FakeStorage

function callsTo(route: string): (string | null)[] {
  return calls.filter(call => call.route === route).map(call => call.token)
}

async function load() {
  const client = await import('./client')
  const events: IdentityEvent[] = []
  client.identityEvents.on(event => events.push(event))
  return { ...client, events }
}

beforeEach(() => {
  vi.resetModules()
  local = new FakeStorage()
  session = new FakeStorage()
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('sessionStorage', session)
  validTokens = new Set(['good'])
  calls = []
  unexpected = []
  routes = new Map<string, Handler>([
    ['POST /api/members', () => {
      validTokens.add('issued')
      return json(200, { member: issued, memberToken: 'issued' })
    }],
    ['GET /api/me', token => token !== null && validTokens.has(token) ? json(200, { member: token === 'issued' ? issued : member }) : unauthorized()],
    ['GET /api/rooms', () => json(200, { rooms: [] })],
  ])
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const route = `${init.method} ${path}`
    const token = new Headers(init.headers).get('Authorization')?.replace('Bearer ', '') ?? null
    calls.push({ route, token })
    const handler = routes.get(route)
    if (!handler) {
      unexpected.push(route)
      return json(500, {})
    }
    return handler(token)
  })
})

afterEach(() => {
  expect(unexpected).toEqual([])
  vi.unstubAllGlobals()
})

describe('发送请求', () => {
  it('需要成员身份的请求带上保存的 token，公开的请求不带', async () => {
    local.setItem(memberTokenKey, 'good')
    const { api } = await load()
    await expect(api.getMe()).resolves.toEqual({ member })
    await api.listRooms()
    expect(callsTo('GET /api/me')).toEqual(['good'])
    expect(callsTo('GET /api/rooms')).toEqual([null])
  })

  it('后台请求带上管理 token', async () => {
    session.setItem(adminTokenKey, 'admin')
    routes.set('GET /api/admin/rooms', () => json(200, { rooms: [] }))
    const { api } = await load()
    await api.adminListRooms()
    expect(callsTo('GET /api/admin/rooms')).toEqual(['admin'])
  })

  it('没有 token 时同时发出的请求共用一次创建，创建之后保存下来', async () => {
    const { api } = await load()
    await Promise.all([api.getMe(), api.getMe()])
    expect(callsTo('POST /api/members')).toHaveLength(1)
    expect(callsTo('GET /api/me')).toEqual(['issued', 'issued'])
    expect(local.getItem(memberTokenKey)).toBe('issued')
  })

  it('创建失败之后，下一个请求重新创建', async () => {
    const create = routes.get('POST /api/members') as Handler
    routes.set('POST /api/members', () => errorResponse(500, 'INTERNAL', '创建失败'))
    const { api } = await load()
    await expect(api.getMe()).rejects.toThrow('创建失败')

    routes.set('POST /api/members', create)
    await expect(api.getMe()).resolves.toEqual({ member: issued })
  })

  it('204 没有响应体', async () => {
    local.setItem(memberTokenKey, 'good')
    routes.set('PATCH /api/me', () => new Response(null, { status: 204 }))
    const { api } = await load()
    await expect(api.updateMe({ name: 'alice' })).resolves.toBeUndefined()
  })

  it('错误响应带上协议定义的错误码、文案和状态码', async () => {
    routes.set('GET /api/rooms', () => errorResponse(429, 'RATE_LIMITED', '太频繁了'))
    const { api, ApiError } = await load()
    const error: unknown = await api.listRooms().catch((reason: unknown) => reason)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ code: 'RATE_LIMITED', message: '太频繁了', status: 429 })
  })

  it('入口自己返回的错误没有协议的响应体，给出通用文案', async () => {
    routes.set('GET /api/rooms', () => new Response('<html>Bad Gateway</html>', { status: 502 }))
    const { api } = await load()
    await expect(api.listRooms()).rejects.toMatchObject({ code: null, message: '服务器暂时不可用，请稍后再试。', status: 502 })
  })

  it('连不上服务器时状态码为 0', async () => {
    vi.stubGlobal('fetch', () => Promise.reject(new TypeError('Failed to fetch')))
    const { api } = await load()
    await expect(api.listRooms()).rejects.toMatchObject({ code: null, message: '无法连接服务器，请检查网络后再试。', status: 0 })
  })
})

describe('token 失效时更换身份', () => {
  it('删除被拒绝的 token、取回新身份，被拒绝的请求不重发', async () => {
    local.setItem(memberTokenKey, 'expired')
    routes.set('PATCH /api/me', unauthorized)
    const { api, events, IdentityRenewedError } = await load()
    await expect(api.updateMe({ name: 'alice' })).rejects.toBeInstanceOf(IdentityRenewedError)

    expect(callsTo('PATCH /api/me')).toEqual(['expired'])
    expect(local.getItem(memberTokenKey)).toBe('issued')
    expect(events).toEqual([{ kind: 'rejected' }, { kind: 'renewed', member: issued }])
  })

  it('同时被拒绝的请求只更换一次身份', async () => {
    local.setItem(memberTokenKey, 'expired')
    routes.set('PATCH /api/me', unauthorized)
    const { api, events, IdentityRenewedError } = await load()
    const results = await Promise.allSettled([api.updateMe({ name: 'a' }), api.updateMe({ name: 'b' }), api.getMe()])
    for (const result of results)
      expect(result).toMatchObject({ status: 'rejected', reason: expect.any(IdentityRenewedError) as unknown })

    expect(callsTo('POST /api/members')).toHaveLength(1)
    expect(events.filter(event => event.kind === 'renewed')).toHaveLength(1)
  })

  it('更换完成之后才到达的拒绝不再更换', async () => {
    local.setItem(memberTokenKey, 'expired')
    const late = deferred<Response>()
    const responses = [late.promise, Promise.resolve(unauthorized())]
    routes.set('PATCH /api/me', () => responses.shift() as Promise<Response>)
    const { api, events, IdentityRenewedError } = await load()
    const delayed = api.updateMe({ name: 'late' })
    await expect(api.updateMe({ name: 'a' })).rejects.toBeInstanceOf(IdentityRenewedError)

    late.resolve(unauthorized())
    await expect(delayed).rejects.toBeInstanceOf(IdentityRenewedError)
    expect(callsTo('POST /api/members')).toHaveLength(1)
    expect(events.filter(event => event.kind === 'rejected')).toHaveLength(1)
    expect(local.getItem(memberTokenKey)).toBe('issued')
  })

  it('其他标签页已经换了 token 时不删除新的 token', async () => {
    local.setItem(memberTokenKey, 'expired')
    routes.set('PATCH /api/me', () => {
      local.setItem(memberTokenKey, 'good')
      return unauthorized()
    })
    const { api, events } = await load()
    await expect(api.updateMe({ name: 'a' })).rejects.toThrow('刚才的操作没有生效')

    expect(local.getItem(memberTokenKey)).toBe('good')
    expect(callsTo('POST /api/members')).toHaveLength(0)
    expect(events).toEqual([{ kind: 'rejected' }, { kind: 'renewed', member }])
  })

  it('其他标签页换上的 token 也被拒绝时不接着更换，避免循环', async () => {
    local.setItem(memberTokenKey, 'expired')
    routes.set('PATCH /api/me', () => {
      local.setItem(memberTokenKey, 'also-expired')
      return unauthorized()
    })
    const { api, events } = await load()
    await expect(api.updateMe({ name: 'a' })).rejects.toMatchObject({ code: 'UNAUTHORIZED' })

    expect(callsTo('GET /api/me')).toEqual(['also-expired'])
    expect(callsTo('POST /api/members')).toHaveLength(0)
    expect(events).toEqual([{ kind: 'rejected' }, { kind: 'renewalFailed' }])
  })

  it('刚获取的 token 被拒绝不说明原来的身份失效，不更换', async () => {
    routes.set('PATCH /api/me', unauthorized)
    const { api, events, IdentityRenewedError } = await load()
    const error: unknown = await api.updateMe({ name: 'a' }).catch((reason: unknown) => reason)
    expect(error).not.toBeInstanceOf(IdentityRenewedError)
    expect(error).toMatchObject({ code: 'UNAUTHORIZED' })
    expect(events).toEqual([])
    expect(local.getItem(memberTokenKey)).toBe('issued')
  })

  it('新 token 也被拒绝时不再更换，避免循环', async () => {
    local.setItem(memberTokenKey, 'expired')
    routes.set('PATCH /api/me', unauthorized)
    routes.set('GET /api/me', unauthorized)
    const { api, events, IdentityRenewedError } = await load()
    const error: unknown = await api.updateMe({ name: 'a' }).catch((reason: unknown) => reason)
    expect(error).not.toBeInstanceOf(IdentityRenewedError)
    expect(error).toMatchObject({ code: 'UNAUTHORIZED' })

    expect(callsTo('GET /api/me')).toEqual(['issued'])
    expect(events).toEqual([{ kind: 'rejected' }, { kind: 'renewalFailed' }])
  })

  it('取不回新身份时报告失败，把原因交给调用方', async () => {
    local.setItem(memberTokenKey, 'expired')
    routes.set('PATCH /api/me', unauthorized)
    routes.set('POST /api/members', () => errorResponse(503, 'UNAVAILABLE', '暂时不可用'))
    const { api, events } = await load()
    await expect(api.updateMe({ name: 'a' })).rejects.toMatchObject({ code: 'UNAVAILABLE' })
    expect(events).toEqual([{ kind: 'rejected' }, { kind: 'renewalFailed' }])
    expect(local.getItem(memberTokenKey)).toBeNull()
  })

  it('其他错误不更换身份', async () => {
    local.setItem(memberTokenKey, 'good')
    routes.set('PATCH /api/me', () => errorResponse(403, 'FORBIDDEN', '没有权限'))
    const { api, events } = await load()
    await expect(api.updateMe({ name: 'a' })).rejects.toMatchObject({ code: 'FORBIDDEN' })
    expect(events).toEqual([])
    expect(local.getItem(memberTokenKey)).toBe('good')
  })
})

describe('换取票据', () => {
  const route = 'POST /api/rooms/r1/tickets'

  it('成功时不核对身份', async () => {
    local.setItem(memberTokenKey, 'good')
    routes.set(route, () => json(200, { ticket: 't1' }))
    const { api } = await load()
    await expect(api.createTicket('r1', {})).resolves.toEqual({ ticket: 't1' })
    expect(callsTo('GET /api/me')).toEqual([])
  })

  it('token 还有效时，UNAUTHORIZED 是房间密码错误，不更换身份', async () => {
    local.setItem(memberTokenKey, 'good')
    routes.set(route, wrongPassword)
    const { api, events } = await load()
    await expect(api.createTicket('r1', { password: 'x' })).rejects.toThrow('房间密码错误')

    expect(callsTo('GET /api/me')).toEqual(['good'])
    expect(events).toEqual([])
    expect(local.getItem(memberTokenKey)).toBe('good')
  })

  it('token 已经失效时更换身份', async () => {
    local.setItem(memberTokenKey, 'expired')
    routes.set(route, unauthorized)
    const { api, events, IdentityRenewedError } = await load()
    await expect(api.createTicket('r1', {})).rejects.toBeInstanceOf(IdentityRenewedError)

    expect(local.getItem(memberTokenKey)).toBe('issued')
    expect(events).toEqual([{ kind: 'rejected' }, { kind: 'renewed', member: issued }])
  })

  it('核对身份时连不上服务器，按原来的错误处理，不动 token', async () => {
    local.setItem(memberTokenKey, 'good')
    routes.set(route, wrongPassword)
    routes.set('GET /api/me', () => errorResponse(503, 'UNAVAILABLE', '暂时不可用'))
    const { api, events } = await load()
    await expect(api.createTicket('r1', {})).rejects.toThrow('房间密码错误')
    expect(events).toEqual([])
    expect(local.getItem(memberTokenKey)).toBe('good')
  })

  it('其他错误不核对身份', async () => {
    local.setItem(memberTokenKey, 'good')
    routes.set(route, () => errorResponse(404, 'NOT_FOUND', '房间不存在'))
    const { api } = await load()
    await expect(api.createTicket('r1', {})).rejects.toThrow('房间不存在')
    expect(callsTo('GET /api/me')).toEqual([])
  })

  it('没有保存的 token 时，刚获取的 token 不用核对', async () => {
    routes.set(route, wrongPassword)
    const { api } = await load()
    await expect(api.createTicket('r1', {})).rejects.toThrow('房间密码错误')
    expect(callsTo('GET /api/me')).toEqual([])
  })
})
