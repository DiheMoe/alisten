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

let session: FakeStorage
let requests: { request: string, body: unknown }[]

beforeEach(() => {
  vi.resetModules()
  session = new FakeStorage()
  requests = []
  vi.stubGlobal('sessionStorage', session)
  vi.stubGlobal('localStorage', new FakeStorage())
  vi.stubGlobal('window', new EventTarget())
  localStorage.setItem('alisten.member-token', 'token')
  vi.stubGlobal('fetch', async (path: string, init: RequestInit) => {
    const request = `${init.method} ${path}`
    requests.push({ request, body: init.body ? JSON.parse(String(init.body)) : undefined })
    if (request === 'POST /api/auth/github')
      return new Response(JSON.stringify({ url: 'https://github.com/login/oauth/authorize?state=s' }), { status: 201 })
    if (request === 'POST /api/auth/github/exchange')
      return new Response(JSON.stringify({ member: { id: 'm1', name: 'octocat', avatarHash: '' }, account: { github: true } }), { status: 200 })
    return new Response(null, { status: 404 })
  })
})

describe('发起授权', () => {
  it('生成随机的 nonce，存在本标签页，随请求带给服务端', async () => {
    const { startGithubLogin } = await import('./github')
    const url = await startGithubLogin('bind')
    expect(url).toBe('https://github.com/login/oauth/authorize?state=s')
    const nonce = session.getItem('alisten.github-nonce')
    expect(nonce).toMatch(/^[\w-]{43}$/)
    expect(requests).toEqual([{ request: 'POST /api/auth/github', body: { intent: 'bind', nonce } }])
    await startGithubLogin('login')
    expect(session.getItem('alisten.github-nonce')).not.toBe(nonce)
  })

  it('绑定时带上用户输入的当前密码，其他意图不带', async () => {
    const { startGithubLogin } = await import('./github')
    await startGithubLogin('bind', 'correct horse')
    await startGithubLogin('verify')
    expect(requests.map(r => r.body)).toEqual([
      { intent: 'bind', nonce: expect.any(String), currentPassword: 'correct horse' },
      { intent: 'verify', nonce: expect.any(String) },
    ])
  })
})

describe('用 GitHub 确认身份之后设置用户名和密码', () => {
  it('意图和 nonce 一起存在本标签页，落地时一起取出', async () => {
    const { startGithubLogin, takeGithubLanding } = await import('./github')
    await startGithubLogin('verify')
    const nonce = session.getItem('alisten.github-nonce')!
    expect(takeGithubLanding('code=abc')).toEqual({ kind: 'exchange', code: 'abc', nonce, intent: 'verify' })
    expect(session.getItem('alisten.github-intent')).toBeNull()
  })

  it('确认成功之后，设置页打开一次设置用户名和密码的对话框', async () => {
    const { finishGithubLogin, takeCredentialsPrompt } = await import('./github')
    expect(takeCredentialsPrompt()).toBe(false)
    await finishGithubLogin('abc', 'n', 'verify')
    expect(takeCredentialsPrompt()).toBe(true)
    expect(takeCredentialsPrompt()).toBe(false)
  })

  it('登录、绑定，以及确认失败时不打开', async () => {
    const { finishGithubLogin, takeCredentialsPrompt } = await import('./github')
    await finishGithubLogin('login-code', 'n', 'login')
    await finishGithubLogin('bind-code', 'n', 'bind')
    expect(takeCredentialsPrompt()).toBe(false)
    vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ error: { code: 'INVALID_CREDENTIALS', message: '需要用这个账号绑定的 GitHub 确认' } }), { status: 401 }))
    await expect(finishGithubLogin('other-code', 'n', 'verify')).rejects.toThrow('需要用这个账号绑定的 GitHub 确认')
    expect(takeCredentialsPrompt()).toBe(false)
  })
})

describe('落地页', () => {
  it('兑换码和本标签页保存的 nonce 一起交给兑换，nonce 取出即删除', async () => {
    const { startGithubLogin, takeGithubLanding } = await import('./github')
    await startGithubLogin('login')
    const nonce = session.getItem('alisten.github-nonce')!
    expect(takeGithubLanding('code=abc')).toEqual({ kind: 'exchange', code: 'abc', nonce, intent: 'login' })
    expect(session.getItem('alisten.github-nonce')).toBeNull()
    // 同一次落地重复读取（例如开发模式下组件挂载两次）得到同样的结果。
    expect(takeGithubLanding('code=abc')).toEqual({ kind: 'exchange', code: 'abc', nonce, intent: 'login' })
  })

  it('本标签页没有发起过授权时不兑换：别人发来的落地页链接换不到会话', async () => {
    const { takeGithubLanding } = await import('./github')
    expect(takeGithubLanding('code=abc')).toEqual({ kind: 'failed', message: '这次 GitHub 登录不是在这个页面发起的，请回到设置重新发起。' })
  })

  it.each([
    ['error=denied', '你在 GitHub 上取消了授权。'],
    ['error=expired', 'GitHub 授权已经过期，请回到设置重新发起。'],
    ['error=failed', '没有从 GitHub 确认你的身份，请稍后再试。'],
    ['error=unknown', '没有从 GitHub 确认你的身份，请稍后再试。'],
    ['', '没有收到 GitHub 的授权结果，请回到设置重新发起。'],
  ])('%s', async (fragment, message) => {
    const { startGithubLogin, takeGithubLanding } = await import('./github')
    await startGithubLogin('login')
    expect(takeGithubLanding(fragment)).toEqual({ kind: 'failed', message })
    expect(session.getItem('alisten.github-nonce')).toBeNull()
  })

  it('同一个兑换码只兑换一次', async () => {
    const { finishGithubLogin } = await import('./github')
    const [first, second] = await Promise.all([finishGithubLogin('abc', 'n', 'login'), finishGithubLogin('abc', 'n', 'login')])
    expect(first).toBe(second)
    expect(requests).toEqual([{ request: 'POST /api/auth/github/exchange', body: { code: 'abc', nonce: 'n' } }])
  })
})
