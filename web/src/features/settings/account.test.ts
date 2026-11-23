import type { GitHubIntent } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeStorage } from '@/core/api/fake-storage'

const memberTokenKey = 'alisten.member-token'
const member = { id: 'm1', name: 'alice', avatarHash: '' }
const guest = { id: 'm2', name: '游客-1a2b', avatarHash: '' }

type Route = () => Response | Promise<Response>

let local: FakeStorage
let routes: Map<string, Route>
let requests: { request: string, body: unknown }[]
// 页面跳转、重新加载和调用方回调的先后顺序。
let events: string[]

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status })
}

function failure(status: number, code: string, message: string): Route {
  return () => json(status, { error: { code, message } })
}

// 请求以原来的身份发出，被服务端拒绝，客户端随即换成新的游客。
function rejectIdentity(request: string): void {
  routes.set(request, failure(401, 'UNAUTHORIZED', '身份已失效'))
  routes.set('POST /api/members', () => json(200, { member: guest, memberToken: 'guest' }))
  routes.set('GET /api/me', () => json(200, { member: guest }))
}

async function load() {
  const account = await import('./account')
  const { profileStore } = await import('@/core/profile')
  const { notificationStore } = await import('@/shared/lib/notification-store')
  const notifications = () => notificationStore.state.notifications.map(({ tone, message }) => ({ tone, message }))
  return { ...account, profileStore, notifications }
}

beforeEach(() => {
  vi.resetModules()
  // 通知按计时器消失。
  vi.useFakeTimers()
  local = new FakeStorage()
  routes = new Map()
  requests = []
  events = []
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('sessionStorage', new FakeStorage())
  vi.stubGlobal('window', { location: { reload: () => events.push('reload'), assign: (url: string) => events.push(`assign ${url}`) } })
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const request = `${init.method} ${path}`
    requests.push({ request, body: init.body ? JSON.parse(String(init.body)) : undefined })
    const route = routes.get(request)
    if (!route)
      throw new Error(`没有预期的请求 ${request}`)
    return route()
  })
  local.setItem(memberTokenKey, 'account')
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('注册和补设用户名密码', () => {
  beforeEach(() => {
    local.setItem(memberTokenKey, 'guest')
  })

  it.each([
    ['register', '已注册'],
    ['credentials', '已设置用户名和密码，其他设备已退出登录'],
  ] as const)('%s 成功后设置账号并关闭对话框', async (mode, message) => {
    routes.set('POST /api/account', () => json(201, { account: { username: 'alice', github: mode === 'credentials' } }))
    const { submitCredentials, profileStore, notifications } = await load()
    expect(await submitCredentials(mode, 'alice', 'correct horse')).toBe('close')
    expect(requests).toEqual([{ request: 'POST /api/account', body: { username: 'alice', password: 'correct horse' } }])
    expect(profileStore.state.account).toEqual({ username: 'alice', github: mode === 'credentials' })
    expect(notifications()).toEqual([{ tone: 'success', message }])
    expect(events).toEqual([])
  })

  it('用 GitHub 确认身份已经过期时提示从按钮重新确认，并关闭对话框', async () => {
    routes.set('POST /api/account', failure(403, 'FORBIDDEN', '需要先用 GitHub 确认身份'))
    const { submitCredentials, profileStore, notifications } = await load()
    expect(await submitCredentials('credentials', 'alice', 'correct horse')).toBe('close')
    expect(notifications()).toEqual([{ tone: 'error', message: '用 GitHub 确认身份已经超过 10 分钟，请在“用户名和密码”一行再点一次“设置”。' }])
    expect(profileStore.state.account).toBeNull()
  })

  it('注册时被拒绝不算确认过期，留着对话框重试', async () => {
    routes.set('POST /api/account', failure(403, 'FORBIDDEN', '暂不开放注册'))
    const { submitCredentials, notifications } = await load()
    expect(await submitCredentials('register', 'alice', 'correct horse')).toBe('retry')
    expect(notifications()).toEqual([{ tone: 'error', message: '暂不开放注册' }])
  })

  it.each(['register', 'credentials'] as const)('%s 的用户名或密码不合格时说明规则，留着对话框重试', async (mode) => {
    routes.set('POST /api/account', failure(400, 'INVALID_ARGUMENT', 'username: invalid'))
    const { submitCredentials, profileStore, notifications } = await load()
    expect(await submitCredentials(mode, 'a', 'short')).toBe('retry')
    expect(notifications()).toEqual([{ tone: 'error', message: '用户名要 3–32 位小写字母、数字、_ 或 -，密码要 8–128 位。' }])
    expect(profileStore.state.account).toBeNull()
  })

  it('身份更换之后提示没有注册成功', async () => {
    rejectIdentity('POST /api/account')
    const { submitCredentials, notifications } = await load()
    expect(await submitCredentials('register', 'alice', 'correct horse')).toBe('retry')
    expect(notifications()).toEqual([{ tone: 'error', message: '没有注册成功，再试一次。' }])
  })
})

describe('用密码登录', () => {
  it('成功后换成账号的 token 并重新加载页面', async () => {
    local.setItem(memberTokenKey, 'guest')
    routes.set('POST /api/sessions', () => json(201, { memberToken: 'account', member, account: { username: 'alice', github: false }, device: 'd'.repeat(43) }))
    const { submitCredentials, notifications } = await load()
    expect(await submitCredentials('login', 'alice', 'correct horse')).toBe('reloading')
    expect(local.getItem(memberTokenKey)).toBe('account')
    expect(events).toEqual(['reload'])
    expect(notifications()).toEqual([])
  })

  it('用户名或密码错误时给出服务端的原因，留着对话框重试', async () => {
    local.setItem(memberTokenKey, 'guest')
    routes.set('POST /api/sessions', failure(401, 'INVALID_CREDENTIALS', '用户名或密码错误'))
    const { submitCredentials, notifications } = await load()
    expect(await submitCredentials('login', 'alice', 'wrong')).toBe('retry')
    expect(notifications()).toEqual([{ tone: 'error', message: '用户名或密码错误' }])
    expect(local.getItem(memberTokenKey)).toBe('guest')
    expect(events).toEqual([])
  })

  it('被拒绝时不算确认过期，留着对话框重试', async () => {
    routes.set('POST /api/sessions', failure(403, 'FORBIDDEN', '这个账号已停用'))
    const { submitCredentials, notifications } = await load()
    expect(await submitCredentials('login', 'alice', 'correct horse')).toBe('retry')
    expect(notifications()).toEqual([{ tone: 'error', message: '这个账号已停用' }])
  })

  it('身份更换之后提示没有登录', async () => {
    rejectIdentity('POST /api/sessions')
    const { submitCredentials, notifications } = await load()
    expect(await submitCredentials('login', 'alice', 'correct horse')).toBe('retry')
    expect(notifications()).toEqual([{ tone: 'error', message: '没有登录，再试一次。' }])
    expect(local.getItem(memberTokenKey)).toBe('guest')
  })
})

describe('修改密码', () => {
  it('成功后提示其他设备已退出登录', async () => {
    routes.set('PUT /api/account/password', () => new Response(null, { status: 204 }))
    const { changePassword, notifications } = await load()
    expect(await changePassword('old password', 'new password')).toBe(true)
    expect(requests).toEqual([{ request: 'PUT /api/account/password', body: { currentPassword: 'old password', newPassword: 'new password' } }])
    expect(notifications()).toEqual([{ tone: 'success', message: '已修改密码，其他设备已退出登录' }])
  })

  it.each([
    [failure(400, 'INVALID_ARGUMENT', 'newPassword: too short'), '密码要 8 到 128 位。'],
    [failure(401, 'INVALID_CREDENTIALS', '当前密码不对'), '当前密码不对'],
  ])('失败时给出原因', async (route, message) => {
    routes.set('PUT /api/account/password', route)
    const { changePassword, notifications } = await load()
    expect(await changePassword('old password', 'new')).toBe(false)
    expect(notifications()).toEqual([{ tone: 'error', message }])
  })

  it('身份更换之后提示密码没有修改', async () => {
    rejectIdentity('PUT /api/account/password')
    const { changePassword, notifications } = await load()
    expect(await changePassword('old password', 'new password')).toBe(false)
    expect(notifications()).toEqual([{ tone: 'error', message: '密码没有修改，再试一次。' }])
  })
})

describe('跳到 GitHub', () => {
  it('成功后跳到授权地址', async () => {
    routes.set('POST /api/auth/github', () => json(201, { url: 'https://github.com/login/oauth/authorize?state=s' }))
    const { goToGitHub, notifications } = await load()
    expect(await goToGitHub('bind', 'correct horse')).toBe(true)
    expect(requests.map(r => r.body)).toEqual([{ intent: 'bind', nonce: expect.any(String), currentPassword: 'correct horse' }])
    expect(events).toEqual(['assign https://github.com/login/oauth/authorize?state=s'])
    expect(notifications()).toEqual([])
  })

  it('失败时给出原因，不跳转', async () => {
    routes.set('POST /api/auth/github', failure(401, 'INVALID_CREDENTIALS', '当前密码不对'))
    const { goToGitHub, notifications } = await load()
    expect(await goToGitHub('bind', 'wrong')).toBe(false)
    expect(notifications()).toEqual([{ tone: 'error', message: '当前密码不对' }])
    expect(events).toEqual([])
  })

  it('身份更换之后提示没有跳转', async () => {
    rejectIdentity('POST /api/auth/github')
    const { goToGitHub, notifications } = await load()
    expect(await goToGitHub('login')).toBe(false)
    expect(notifications()).toEqual([{ tone: 'error', message: '没有跳转到 GitHub，再试一次。' }])
    expect(events).toEqual([])
  })
})

describe('解绑 GitHub', () => {
  async function signedIn() {
    routes.set('GET /api/me', () => json(200, { member, account: { username: 'alice', email: 'alice@example.com', github: true } }))
    const loaded = await load()
    await loaded.profileStore.load()
    requests = []
    return loaded
  }

  it('成功后账号改成没有绑定，其余不变', async () => {
    routes.set('POST /api/account/github/removals', () => new Response(null, { status: 204 }))
    const { unbindGitHub, profileStore, notifications } = await signedIn()
    expect(await unbindGitHub('correct horse')).toBe(true)
    expect(requests).toEqual([{ request: 'POST /api/account/github/removals', body: { currentPassword: 'correct horse' } }])
    expect(profileStore.state.account).toEqual({ username: 'alice', email: 'alice@example.com', github: false })
    expect(notifications()).toEqual([{ tone: 'success', message: '已解绑 GitHub' }])
  })

  it('失败时给出原因，账号不变', async () => {
    routes.set('POST /api/account/github/removals', failure(401, 'INVALID_CREDENTIALS', '当前密码不对'))
    const { unbindGitHub, profileStore, notifications } = await signedIn()
    expect(await unbindGitHub('wrong')).toBe(false)
    expect(profileStore.state.account).toMatchObject({ github: true })
    expect(notifications()).toEqual([{ tone: 'error', message: '当前密码不对' }])
  })

  it('身份更换之后提示没有解绑', async () => {
    const { unbindGitHub, notifications } = await signedIn()
    rejectIdentity('POST /api/account/github/removals')
    expect(await unbindGitHub('correct horse')).toBe(false)
    expect(notifications()).toEqual([{ tone: 'error', message: '没有解绑，再试一次。' }])
  })

  it('没有账号时不发请求', async () => {
    const { unbindGitHub, notifications } = await load()
    expect(await unbindGitHub('correct horse')).toBe(false)
    expect(requests).toEqual([])
    expect(notifications()).toEqual([])
  })
})

describe('退出登录', () => {
  it('成功后删除 token 并重新加载页面', async () => {
    routes.set('DELETE /api/sessions/current', () => new Response(null, { status: 204 }))
    const { logout } = await load()
    expect(await logout()).toBe(true)
    expect(local.getItem(memberTokenKey)).toBeNull()
    expect(events).toEqual(['reload'])
  })

  it('会话已经失效、客户端换成了新的游客时，重新加载页面', async () => {
    rejectIdentity('DELETE /api/sessions/current')
    const { logout } = await load()
    expect(await logout()).toBe(true)
    expect(local.getItem(memberTokenKey)).toBe('guest')
    expect(events).toEqual(['reload'])
  })

  it('其他错误不重新加载页面，token 保留', async () => {
    routes.set('DELETE /api/sessions/current', failure(500, 'INTERNAL', '出错了'))
    const { logout, notifications } = await load()
    expect(await logout()).toBe(false)
    expect(local.getItem(memberTokenKey)).toBe('account')
    expect(events).toEqual([])
    expect(notifications()).toEqual([{ tone: 'error', message: '出错了' }])
  })
})

describe('在落地页上兑换 GitHub 授权', () => {
  const exchange = 'POST /api/auth/github/exchange'
  const landing = (intent: GitHubIntent) => ({ kind: 'exchange', code: 'abc', nonce: 'n', intent }) as const

  async function complete(intent: GitHubIntent) {
    const loaded = await load()
    const result = await loaded.completeGitHubLogin(landing(intent), new AbortController().signal, () => events.push('done'))
    return { ...loaded, result }
  }

  it('登录了另一个账号时先交给调用方，再换身份、重新加载页面', async () => {
    local.setItem(memberTokenKey, 'guest')
    routes.set(exchange, () => json(200, { member, account: { username: 'alice', github: true }, memberToken: 'account' }))
    const { result, notifications } = await complete('login')
    expect(result).toBeNull()
    expect(events).toEqual(['done', 'reload'])
    expect(local.getItem(memberTokenKey)).toBe('account')
    expect(notifications()).toEqual([])
  })

  it('确认身份之后不改账号，直接交给调用方', async () => {
    routes.set(exchange, () => json(200, { member, account: { github: true } }))
    const { result, profileStore, notifications } = await complete('verify')
    expect(result).toBeNull()
    expect(events).toEqual(['done'])
    expect(profileStore.state.account).toBeNull()
    expect(notifications()).toEqual([])
  })

  it.each([
    ['用 GitHub 注册', 'login', { github: true }, '已用 GitHub 注册'],
    ['绑定', 'bind', { username: 'alice', github: true }, '已绑定 GitHub'],
  ] as const)('%s之后设置账号，不换身份', async (_, intent, account, message) => {
    routes.set(exchange, () => json(200, { member, account }))
    const { result, profileStore, notifications } = await complete(intent)
    expect(result).toBeNull()
    expect(profileStore.state.account).toEqual(account)
    expect(notifications()).toEqual([{ tone: 'success', message }])
    expect(events).toEqual(['done'])
    expect(local.getItem(memberTokenKey)).toBe('account')
  })

  it.each([
    [() => routes.set(exchange, failure(401, 'INVALID_CREDENTIALS', '需要用这个账号绑定的 GitHub 确认')), '需要用这个账号绑定的 GitHub 确认'],
    [() => rejectIdentity(exchange), 'GitHub 登录没有完成，请回到设置重新发起。'],
  ])('失败时返回原因，不交给调用方', async (fail, message) => {
    fail()
    const { result, profileStore } = await complete('bind')
    expect(result).toBe(message)
    expect(events).toEqual([])
    expect(profileStore.state.account).toBeNull()
  })

  it.each([
    ['成功', () => json(200, { member, account: { github: true } })],
    ['失败', failure(503, 'UNAVAILABLE', '服务暂时不可用')],
  ])('离开落地页之后%s的结果作废', async (_, route) => {
    routes.set(exchange, route)
    const { completeGitHubLogin, profileStore, notifications } = await load()
    const leaving = new AbortController()
    const result = completeGitHubLogin(landing('login'), leaving.signal, () => events.push('done'))
    leaving.abort()
    expect(await result).toBeNull()
    expect(events).toEqual([])
    expect(profileStore.state.account).toBeNull()
    expect(notifications()).toEqual([])
  })
})

describe('配对码', () => {
  it('生成失败时抛出给用户看的原因', async () => {
    routes.set('POST /api/account/pairing-codes', failure(503, 'UNAVAILABLE', '服务暂时不可用'))
    const { createPairingCode } = await load()
    await expect(createPairingCode()).rejects.toThrow('服务暂时不可用')
    rejectIdentity('POST /api/account/pairing-codes')
    await expect(createPairingCode()).rejects.toThrow('没有生成配对码，再试一次。')
  })

  it('格式不对时提示规则，不发请求', async () => {
    const { checkPairingCode, notifications } = await load()
    expect(checkPairingCode('ABCD2345')).toBe(true)
    expect(notifications()).toEqual([])
    expect(checkPairingCode('ABCD0123')).toBe(false)
    expect(notifications()).toEqual([{ tone: 'error', message: '配对码是 8 位字母和数字，不含 0、O、1、I。' }])
    expect(requests).toEqual([])
  })

  it('兑换成功时返回新会话的 token，不自己换身份', async () => {
    local.setItem(memberTokenKey, 'guest')
    routes.set('POST /api/sessions/pairing', () => json(201, { memberToken: 'account', member, account: { username: 'alice', github: false } }))
    const { redeemPairingCode } = await load()
    expect(await redeemPairingCode('ABCD2345')).toBe('account')
    expect(requests.map(r => r.body)).toEqual([{ code: 'ABCD2345' }])
    expect(local.getItem(memberTokenKey)).toBe('guest')
    expect(events).toEqual([])
  })

  it.each([
    [failure(400, 'INVALID_ARGUMENT', 'code: invalid'), '配对码是 8 位字母和数字，不含 0、O、1、I。'],
    [failure(404, 'NOT_FOUND', '配对码不存在或已经用过'), '配对码不存在或已经用过'],
  ])('兑换失败时给出原因', async (route, message) => {
    local.setItem(memberTokenKey, 'guest')
    routes.set('POST /api/sessions/pairing', route)
    const { redeemPairingCode, notifications } = await load()
    expect(await redeemPairingCode('ABCD2345')).toBeNull()
    expect(notifications()).toEqual([{ tone: 'error', message }])
  })

  it('身份更换之后提示没有登录', async () => {
    rejectIdentity('POST /api/sessions/pairing')
    const { redeemPairingCode, notifications } = await load()
    expect(await redeemPairingCode('ABCD2345')).toBeNull()
    expect(notifications()).toEqual([{ tone: 'error', message: '没有登录，再试一次。' }])
  })
})
