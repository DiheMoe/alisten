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

const memberTokenKey = 'alisten.member-token'
const emailKey = 'alisten.email'
const member = { id: 'm1', name: 'alice', avatarHash: '' }
const renewed = { id: 'm2', name: '游客-1a2b', avatarHash: '' }

let local: FakeStorage
let storageListeners: ((event: { key: string }) => void)[]
let reload: ReturnType<typeof vi.fn>
let me: unknown
let updates: unknown[]
let patchFails: boolean
let patchError: { status: number, code: string } | null
let extraRoutes: Map<string, () => Response>

async function load() {
  const [{ profileStore, switchIdentity }, { identityEvents }, { notificationStore }] = await Promise.all([import('./profile-store'), import('@/core/api'), import('@/shared/ui')])
  return { profileStore, switchIdentity, identityEvents, notificationStore }
}

beforeEach(() => {
  vi.resetModules()
  // 通知按计时器消失。
  vi.useFakeTimers()
  local = new FakeStorage()
  reload = vi.fn()
  me = { member }
  updates = []
  patchFails = false
  patchError = null
  extraRoutes = new Map()
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('sessionStorage', new FakeStorage())
  storageListeners = []
  vi.stubGlobal('window', { location: { reload }, addEventListener: (_type: string, listener: (event: { key: string }) => void) => storageListeners.push(listener) })
  vi.stubGlobal('fetch', (path: string, init: RequestInit) => {
    const request = `${init.method} ${path}`
    if (request === 'PATCH /api/me') {
      if (patchFails)
        return Promise.reject(new TypeError('offline'))
      updates.push(JSON.parse(String(init.body)))
      if (patchError)
        return Promise.resolve(new Response(JSON.stringify({ error: { code: patchError.code, message: '邮箱格式不对' } }), { status: patchError.status }))
      return Promise.resolve(new Response(JSON.stringify({ member }), { status: 200 }))
    }
    const extra = extraRoutes.get(request)
    if (extra)
      return Promise.resolve(extra())
    if (request !== 'GET /api/me')
      throw new Error(`没有预期的请求 ${request}`)
    return Promise.resolve(new Response(JSON.stringify(me), { status: 200 }))
  })
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('switchIdentity', () => {
  it('换成新的 token，删除所有票据和本机的邮箱，重新加载页面', async () => {
    local.setItem(memberTokenKey, 'guest')
    local.setItem('alisten.ticket.r1', 't1')
    local.setItem('alisten.ticket.r2', 't2')
    local.setItem(emailKey, 'alice@example.com')
    local.setItem('alisten.volume', '80')
    const { switchIdentity } = await load()
    switchIdentity('account')

    expect(local.getItem(memberTokenKey)).toBe('account')
    expect(local.getItem('alisten.ticket.r1')).toBeNull()
    expect(local.getItem('alisten.ticket.r2')).toBeNull()
    expect(local.getItem(emailKey)).toBeNull()
    expect(local.getItem('alisten.volume')).toBe('80')
    expect(reload).toHaveBeenCalledOnce()
  })

  it('退出登录时删除 token，重新加载之后换成新的游客；登录设备凭证保留', async () => {
    const devices = JSON.stringify({ alice: 'a'.repeat(43) })
    local.setItem(memberTokenKey, 'account')
    local.setItem('alisten.ticket.r1', 't1')
    local.setItem(emailKey, 'alice@example.com')
    local.setItem('alisten.login-devices', devices)
    const { switchIdentity } = await load()
    switchIdentity(null)

    expect(local.getItem(memberTokenKey)).toBeNull()
    expect(local.getItem('alisten.ticket.r1')).toBeNull()
    expect(local.getItem(emailKey)).toBeNull()
    expect(local.getItem('alisten.login-devices')).toBe(devices)
    expect(reload).toHaveBeenCalledOnce()
  })
})

describe('其他标签页更换身份', () => {
  it('其他标签页换了 token 之后重新加载页面', async () => {
    local.setItem(memberTokenKey, 'good')
    const { profileStore } = await load()
    profileStore.start()
    await profileStore.load()
    local.setItem(memberTokenKey, 'other')
    for (const listener of storageListeners) listener({ key: memberTokenKey })
    expect(reload).toHaveBeenCalledOnce()
  })
})

describe('退出登录', () => {
  beforeEach(() => {
    local.setItem(memberTokenKey, 'account')
  })

  it('成功后删除 token 并重新加载页面', async () => {
    extraRoutes.set('DELETE /api/sessions/current', () => new Response(null, { status: 204 }))
    const { profileStore } = await load()
    await profileStore.logout()
    expect(local.getItem(memberTokenKey)).toBeNull()
    expect(reload).toHaveBeenCalledOnce()
  })

  it('会话已经失效、客户端换成了新的游客时，重新加载页面', async () => {
    extraRoutes.set('DELETE /api/sessions/current', () => new Response(JSON.stringify({ error: { code: 'UNAUTHORIZED', message: '身份已失效' } }), { status: 401 }))
    extraRoutes.set('POST /api/members', () => new Response(JSON.stringify({ member: renewed, memberToken: 'guest' }), { status: 200 }))
    me = { member: renewed }
    const { profileStore } = await load()
    await profileStore.logout()
    expect(local.getItem(memberTokenKey)).toBe('guest')
    expect(reload).toHaveBeenCalledOnce()
  })

  it('其他错误不重新加载页面，token 保留', async () => {
    extraRoutes.set('DELETE /api/sessions/current', () => new Response(JSON.stringify({ error: { code: 'INTERNAL', message: '出错了' } }), { status: 500 }))
    const { profileStore } = await load()
    expect(await profileStore.logout()).toBe(false)
    expect(local.getItem(memberTokenKey)).toBe('account')
    expect(reload).not.toHaveBeenCalled()
  })
})

describe('账号状态', () => {
  beforeEach(() => {
    local.setItem(memberTokenKey, 'good')
  })

  it('取回资料时一并取回账号', async () => {
    me = { member, account: { username: 'alice', github: false } }
    const { profileStore } = await load()
    await profileStore.load()
    expect(profileStore.state).toMatchObject({ member, account: { username: 'alice', github: false } })
  })

  it('游客没有账号', async () => {
    const { profileStore } = await load()
    await profileStore.registered({ username: 'alice', github: false })
    await profileStore.load()
    expect(profileStore.state).toMatchObject({ member, account: null })
  })

  it('注册之后设置账号', async () => {
    const { profileStore } = await load()
    await profileStore.load()
    await profileStore.registered({ username: 'alice', github: false })
    expect(profileStore.state).toMatchObject({ member, account: { username: 'alice', github: false } })
  })

  it('身份被拒绝或更换之后没有账号', async () => {
    const { profileStore, identityEvents } = await load()
    profileStore.start()
    await profileStore.registered({ username: 'alice', github: false })
    identityEvents.emit({ kind: 'rejected' })
    expect(profileStore.state).toMatchObject({ member: null, account: null })

    await profileStore.registered({ username: 'alice', github: false })
    identityEvents.emit({ kind: 'renewed', member: renewed })
    expect(profileStore.state).toMatchObject({ member: renewed, account: null })
  })
})

describe('邮箱', () => {
  beforeEach(() => {
    local.setItem(memberTokenKey, 'good')
  })

  it('已注册时取自账号，不读本机', async () => {
    local.setItem(emailKey, 'local@example.com')
    me = { member, account: { username: 'alice', email: 'Alice@Example.COM', github: false } }
    const { profileStore } = await load()
    profileStore.start()
    await profileStore.load()
    expect(profileStore.state.email).toBe('Alice@Example.COM')

    local.removeItem(emailKey)
    me = { member, account: { username: 'alice', github: false } }
    await profileStore.load()
    expect(profileStore.state.email).toBe('')
  })

  it('游客取自本机', async () => {
    local.setItem(emailKey, 'guest@example.com')
    const { profileStore } = await load()
    profileStore.start()
    await profileStore.load()
    expect(profileStore.state.email).toBe('guest@example.com')
  })

  it('已注册时保存到账号，不写本机', async () => {
    me = { member, account: { username: 'alice', github: false } }
    const { profileStore } = await load()
    await profileStore.load()

    await profileStore.save({ email: ' Alice@Example.COM ' })
    expect(updates).toEqual([{ email: ' Alice@Example.COM ' }])
    expect(profileStore.state).toMatchObject({ email: 'Alice@Example.COM', account: { username: 'alice', email: 'Alice@Example.COM', github: false } })
    expect(local.getItem(emailKey)).toBeNull()

    await profileStore.save({ email: '' })
    expect(profileStore.state.email).toBe('')
    expect(profileStore.state.account).toEqual({ username: 'alice', github: false })
  })

  it('游客保存到本机', async () => {
    const { profileStore } = await load()
    await profileStore.load()
    await profileStore.save({ email: 'guest@example.com' })
    expect(local.getItem(emailKey)).toBe('guest@example.com')
    expect(profileStore.state.email).toBe('guest@example.com')
  })

  it('注册之后把本机的邮箱存进账号，并删除本机的副本', async () => {
    local.setItem(emailKey, 'guest@example.com')
    const { profileStore } = await load()
    profileStore.start()
    await profileStore.load()
    await profileStore.registered({ username: 'alice', github: false })

    expect(updates).toEqual([{ email: 'guest@example.com' }])
    expect(local.getItem(emailKey)).toBeNull()
    expect(profileStore.state).toMatchObject({ email: 'guest@example.com', account: { username: 'alice', email: 'guest@example.com', github: false } })
  })

  it('注册时本机没有邮箱就不发请求', async () => {
    const { profileStore } = await load()
    await profileStore.load()
    await profileStore.registered({ username: 'alice', github: false })
    expect(updates).toEqual([])
  })

  it('注册时转存失败，本机的副本保留，下次加载时重试并删除副本', async () => {
    local.setItem(emailKey, 'guest@example.com')
    patchFails = true
    const { profileStore } = await load()
    profileStore.start()
    await profileStore.registered({ username: 'alice', github: false })
    expect(local.getItem(emailKey)).toBe('guest@example.com')

    patchFails = false
    me = { member, account: { username: 'alice', github: false } }
    await profileStore.load()
    expect(updates).toEqual([{ email: 'guest@example.com' }])
    expect(local.getItem(emailKey)).toBeNull()
    expect(profileStore.state).toMatchObject({ email: 'guest@example.com', account: { username: 'alice', email: 'guest@example.com', github: false } })
  })

  it('服务端判定本机的邮箱不合格时删除副本，之后加载不再转存', async () => {
    local.setItem(emailKey, 'legacy')
    patchError = { status: 400, code: 'INVALID_ARGUMENT' }
    me = { member, account: { username: 'alice', github: false } }
    const { profileStore } = await load()
    await profileStore.load()
    expect(local.getItem(emailKey)).toBeNull()
    expect(updates).toEqual([{ email: 'legacy' }])

    await profileStore.load()
    expect(updates).toEqual([{ email: 'legacy' }])
  })

  it('重试仍失败时保留本机的副本，下次加载再试', async () => {
    local.setItem(emailKey, 'guest@example.com')
    patchFails = true
    me = { member, account: { username: 'alice', github: false } }
    const { profileStore } = await load()
    await profileStore.load()
    expect(local.getItem(emailKey)).toBe('guest@example.com')

    patchFails = false
    await profileStore.load()
    expect(local.getItem(emailKey)).toBeNull()
    expect(updates).toEqual([{ email: 'guest@example.com' }])
  })

  it('已注册且本机没有副本，或账号已有邮箱时，加载不发更新请求', async () => {
    me = { member, account: { username: 'alice', github: false } }
    const { profileStore } = await load()
    await profileStore.load()

    local.setItem(emailKey, 'guest@example.com')
    me = { member, account: { username: 'alice', email: 'alice@example.com', github: false } }
    await profileStore.load()
    expect(updates).toEqual([])
  })

  it('保存失败时抛出按字段说明的原因，不弹通知，资料不变', async () => {
    me = { member, account: { username: 'alice', email: 'alice@example.com', github: false } }
    const { profileStore, notificationStore } = await load()
    await profileStore.load()

    patchError = { status: 400, code: 'INVALID_ARGUMENT' }
    await expect(profileStore.save({ email: 'alice' })).rejects.toThrow('邮箱要写成 name@example.com 这样的格式。')
    await expect(profileStore.save({ name: '' })).rejects.toThrow('昵称不能为空。')
    patchError = null
    patchFails = true
    await expect(profileStore.save({ name: 'bob' })).rejects.toThrow('无法连接服务器，请检查网络后再试。')

    expect(profileStore.state).toMatchObject({ member, email: 'alice@example.com' })
    expect(notificationStore.state.notifications).toEqual([])
  })

  it('用户清空账号的邮箱时一并删除本机残留的副本，之后加载不再转存', async () => {
    local.setItem(emailKey, 'guest@example.com')
    me = { member, account: { username: 'alice', email: 'alice@example.com', github: false } }
    const { profileStore } = await load()
    await profileStore.load()
    await profileStore.save({ email: '' })
    expect(local.getItem(emailKey)).toBeNull()

    updates.length = 0
    me = { member, account: { username: 'alice', github: false } }
    await profileStore.load()
    expect(updates).toEqual([])
  })
})

describe('记录播放历史', () => {
  beforeEach(() => {
    local.setItem(memberTokenKey, 'good')
  })

  it('取回资料时一并取回开关，修改时调用对应的接口', async () => {
    me = { member, recordListens: false }
    const switched: string[] = []
    extraRoutes.set('PUT /api/me/listen-recording', () => {
      switched.push('on')
      return new Response(null, { status: 204 })
    })
    extraRoutes.set('DELETE /api/me/listen-recording', () => {
      switched.push('off')
      return new Response(null, { status: 204 })
    })
    const { profileStore } = await load()
    expect(profileStore.state.recordListens).toBeNull()
    await profileStore.load()
    expect(profileStore.state.recordListens).toBe(false)
    await profileStore.setRecordListens(true)
    expect(profileStore.state.recordListens).toBe(true)
    await profileStore.setRecordListens(false)
    expect(profileStore.state.recordListens).toBe(false)
    expect(switched).toEqual(['on', 'off'])
  })

  it('修改失败时抛出原因，不弹通知，开关不变', async () => {
    me = { member, recordListens: true }
    extraRoutes.set('DELETE /api/me/listen-recording', () => new Response(JSON.stringify({ error: { code: 'UNAVAILABLE', message: '暂时不可用' } }), { status: 503 }))
    const { profileStore, notificationStore } = await load()
    await profileStore.load()
    await expect(profileStore.setRecordListens(false)).rejects.toThrow('暂时不可用')
    expect(profileStore.state.recordListens).toBe(true)
    expect(notificationStore.state.notifications).toEqual([])
  })

  it('身份被拒绝时开关未知，换成新的游客之后是默认的开启', async () => {
    me = { member, recordListens: false }
    const { profileStore, identityEvents } = await load()
    profileStore.start()
    await profileStore.load()
    identityEvents.emit({ kind: 'rejected' })
    expect(profileStore.state.recordListens).toBeNull()
    identityEvents.emit({ kind: 'renewed', member: renewed })
    expect(profileStore.state.recordListens).toBe(true)
  })
})
