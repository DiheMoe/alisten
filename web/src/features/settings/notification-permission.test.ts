import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NotificationPermissionRequest } from './notification-permission'

// 浏览器的 Notification：每次申请都弹出一个提示，由测试决定用户怎么回应、什么时候回应。
class FakeNotification {
  static permission: NotificationPermission = 'default'
  static prompts: ((permission: NotificationPermission) => void)[] = []

  static requestPermission(): Promise<NotificationPermission> {
    return new Promise((resolve) => {
      FakeNotification.prompts.push((permission) => {
        FakeNotification.permission = permission
        resolve(permission)
      })
    })
  }
}

function answer(index: number, permission: NotificationPermission): void {
  FakeNotification.prompts[index]!(permission)
}

beforeEach(() => {
  FakeNotification.permission = 'default'
  FakeNotification.prompts = []
  vi.stubGlobal('Notification', FakeNotification)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('申请系统通知的权限', () => {
  it('从浏览器读到当前的权限', () => {
    FakeNotification.permission = 'denied'
    expect(new NotificationPermissionRequest().state).toEqual({ permission: 'denied', waiting: false, refused: false })
  })

  it('申请之后等待浏览器回应，允许之后拿到授权', async () => {
    const request = new NotificationPermissionRequest()
    const result = request.request()
    expect(request.state).toEqual({ permission: 'default', waiting: true, refused: false })
    answer(0, 'granted')
    expect(await result).toBe(true)
    expect(request.state).toEqual({ permission: 'granted', waiting: false, refused: false })
  })

  it('浏览器拒绝时没有拿到授权', async () => {
    const request = new NotificationPermissionRequest()
    const result = request.request()
    answer(0, 'denied')
    expect(await result).toBe(false)
    expect(request.state).toEqual({ permission: 'denied', waiting: false, refused: true })
  })

  it('用户关掉提示、权限仍是 default 时，也算没有拿到授权', async () => {
    const request = new NotificationPermissionRequest()
    const result = request.request()
    answer(0, 'default')
    expect(await result).toBe(false)
    expect(request.state).toEqual({ permission: 'default', waiting: false, refused: true })
  })

  it('提示一直不返回时可以放弃，放弃之后提示才返回也不算这次申请的结果，只更新权限', async () => {
    const request = new NotificationPermissionRequest()
    const result = request.request()
    request.abandon()
    expect(request.state).toEqual({ permission: 'default', waiting: false, refused: false })
    answer(0, 'granted')
    expect(await result).toBe(false)
    expect(request.state).toEqual({ permission: 'granted', waiting: false, refused: false })
  })

  it('放弃后被拒绝的提示晚回来，不显示没有拿到授权', async () => {
    const request = new NotificationPermissionRequest()
    const result = request.request()
    request.abandon()
    answer(0, 'denied')
    expect(await result).toBe(false)
    expect(request.state).toEqual({ permission: 'denied', waiting: false, refused: false })
  })

  it('放弃之后重新申请，之前的提示晚回来不结束新的等待', async () => {
    const request = new NotificationPermissionRequest()
    const first = request.request()
    request.abandon()
    const second = request.request()
    answer(0, 'default')
    expect(await first).toBe(false)
    expect(request.state).toEqual({ permission: 'default', waiting: true, refused: false })
    answer(1, 'granted')
    expect(await second).toBe(true)
    expect(request.state).toEqual({ permission: 'granted', waiting: false, refused: false })
  })

  it('再次申请时清掉上次没有拿到授权的说明', async () => {
    const request = new NotificationPermissionRequest()
    const first = request.request()
    answer(0, 'default')
    await first
    void request.request()
    expect(request.state).toEqual({ permission: 'default', waiting: true, refused: false })
  })
})
