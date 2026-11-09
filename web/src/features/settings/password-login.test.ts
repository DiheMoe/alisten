import { beforeEach, describe, expect, it, vi } from 'vitest'

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

const devicesKey = 'alisten.login-devices'

// 服务端发给这个账号的凭证，格式与协议中的 LoginDevice 一致。
function deviceOf(username: string): string {
  return username.padEnd(43, '0')
}

let local: FakeStorage
let bodies: unknown[]
let rejected: boolean

beforeEach(() => {
  vi.resetModules()
  local = new FakeStorage()
  bodies = []
  rejected = false
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('sessionStorage', new FakeStorage())
  vi.stubGlobal('window', new EventTarget())
  local.setItem('alisten.member-token', 'guest')
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    if (`${init.method} ${path}` !== 'POST /api/sessions')
      return new Response(null, { status: 404 })
    const body = JSON.parse(String(init.body)) as { username: string }
    bodies.push(body)
    if (rejected)
      return new Response(JSON.stringify({ error: { code: 'INVALID_CREDENTIALS', message: '用户名或密码错误' } }), { status: 401 })
    const { username } = body
    return new Response(JSON.stringify({
      memberToken: `${username}-token`,
      member: { id: username, name: username, avatarHash: '' },
      account: { username, github: false },
      device: deviceOf(username),
    }), { status: 201 })
  })
})

async function login(username: string): Promise<string> {
  const { loginWithPassword } = await import('./password-login')
  return loginWithPassword(username, 'correct horse')
}

function saved(): unknown {
  return JSON.parse(local.getItem(devicesKey)!)
}

describe('登录设备凭证', () => {
  it('第一次登录不带凭证，保存响应里的凭证；再登录同一个账号时带上它', async () => {
    expect(await login('alice')).toBe('alice-token')
    expect(bodies).toEqual([{ username: 'alice', password: 'correct horse' }])
    expect(saved()).toEqual({ alice: deviceOf('alice') })

    await login('alice')
    expect(bodies[1]).toEqual({ username: 'alice', password: 'correct horse', device: deviceOf('alice') })
  })

  it('按用户名分别保存：登录别的账号不带这个账号的凭证，也不覆盖它', async () => {
    await login('alice')
    await login('bob')
    expect(bodies[1]).toEqual({ username: 'bob', password: 'correct horse' })
    expect(saved()).toEqual({ alice: deviceOf('alice'), bob: deviceOf('bob') })
  })

  it('服务端不认带去的凭证、换发了新的时，保存新的', async () => {
    const stale = 'x'.repeat(43)
    local.setItem(devicesKey, JSON.stringify({ alice: stale }))
    await login('alice')
    expect(bodies).toEqual([{ username: 'alice', password: 'correct horse', device: stale }])
    expect(saved()).toEqual({ alice: deviceOf('alice') })
  })

  it('登录失败时不改动保存的凭证', async () => {
    local.setItem(devicesKey, JSON.stringify({ alice: deviceOf('alice') }))
    rejected = true
    await expect(login('alice')).rejects.toMatchObject({ code: 'INVALID_CREDENTIALS' })
    await expect(login('bob')).rejects.toMatchObject({ code: 'INVALID_CREDENTIALS' })
    expect(saved()).toEqual({ alice: deviceOf('alice') })
  })

  it('保存的内容不是凭证时不带上，登录成功后重新保存', async () => {
    for (const [stored, username] of [['not json', 'alice'], [JSON.stringify({ alice: 'too short' }), 'alice'], ['{}', 'constructor']]) {
      bodies = []
      local.setItem(devicesKey, stored)
      await login(username)
      expect(bodies).toEqual([{ username, password: 'correct horse' }])
      expect(saved()).toEqual({ [username]: deviceOf(username) })
    }
  })
})
