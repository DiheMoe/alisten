import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/core/api'
import { notificationStore } from '@/shared/ui'
import { adminStore } from './admin-store'

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

const messages = () => notificationStore.state.notifications.map(n => n.message)

beforeEach(() => {
  vi.stubGlobal('sessionStorage', new FakeStorage())
  for (const { id } of notificationStore.state.notifications)
    notificationStore.dismiss(id)
  adminStore.signIn('token')
})

describe('adminStore.attempt', () => {
  it('成功时返回结果', async () => {
    await expect(adminStore.attempt(async () => 42)).resolves.toBe(42)
  })

  it('失败时抛出给用户看的原因，不弹通知', async () => {
    const failing = adminStore.attempt(() => Promise.reject(new ApiError('brandName：长度超过 20', 'INVALID_ARGUMENT', 400)), { INVALID_ARGUMENT: '品牌名太长了。' })
    await expect(failing).rejects.toThrow('品牌名太长了。')
    expect(messages()).toEqual([])
    expect(adminStore.state.signedIn).toBe(true)
  })

  it('管理 token 被拒绝时提示并退出登录', async () => {
    const rejected = new ApiError('管理 token 无效', 'UNAUTHORIZED', 401)
    await expect(adminStore.attempt(() => Promise.reject(rejected))).rejects.toThrow('管理 token 无效')
    expect(messages()).toEqual(['管理 token 无效'])
    expect(adminStore.state.signedIn).toBe(false)
    expect(sessionStorage.getItem('alisten.admin-token')).toBeNull()
  })

  it('已经退出登录时不再重复提示', async () => {
    adminStore.signOut()
    await expect(adminStore.attempt(() => Promise.reject(new ApiError('管理 token 无效', 'UNAUTHORIZED', 401)))).rejects.toThrow()
    expect(messages()).toEqual([])
  })
})
