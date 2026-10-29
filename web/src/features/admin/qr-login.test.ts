import type { QrLoginState } from './qr-login'
import { describe, expect, it } from 'vitest'
import { nextQrLogin, startQrLogin } from './qr-login'

const expiresAtMs = 120_000
const waiting = startQrLogin(expiresAtMs)

describe('nextQrLogin', () => {
  it('等待扫码和已扫码时继续查询，并记下平台给出的说明', () => {
    expect(nextQrLogin(waiting, { kind: 'checked', result: { status: 'waiting', message: '' } })).toEqual({ state: waiting, polling: true })
    expect(nextQrLogin(waiting, { kind: 'checked', result: { status: 'scanned', message: '请在手机上确认' } }))
      .toEqual({ state: { status: 'scanned', message: '请在手机上确认', expiresAtMs }, polling: true })
  })

  it('登录成功、二维码过期或登录失败时停止查询', () => {
    for (const status of ['success', 'expired', 'failed'] as const) {
      expect(nextQrLogin(waiting, { kind: 'checked', result: { status, message: '二维码已失效' } }))
        .toEqual({ state: { status, message: '二维码已失效', expiresAtMs }, polling: false })
    }
  })

  it('查询失败时按登录失败处理，停止查询', () => {
    expect(nextQrLogin(waiting, { kind: 'checked', result: null })).toEqual({ state: { status: 'failed', message: '', expiresAtMs }, polling: false })
  })

  it('到了 expiresAt 按过期处理，停止查询', () => {
    expect(nextQrLogin(waiting, { kind: 'tick', nowMs: expiresAtMs - 1 })).toEqual({ state: waiting, polling: true })
    const scanned: QrLoginState = { status: 'scanned', message: '', expiresAtMs }
    expect(nextQrLogin(scanned, { kind: 'tick', nowMs: expiresAtMs })).toEqual({ state: { status: 'expired', message: '', expiresAtMs }, polling: false })
  })

  it('结束之后到达的查询结果和计时都不再改变状态', () => {
    const success: QrLoginState = { status: 'success', message: '', expiresAtMs }
    expect(nextQrLogin(success, { kind: 'tick', nowMs: expiresAtMs })).toEqual({ state: success, polling: false })
    const expired: QrLoginState = { status: 'expired', message: '', expiresAtMs }
    expect(nextQrLogin(expired, { kind: 'checked', result: { status: 'scanned', message: '' } })).toEqual({ state: expired, polling: false })
    expect(nextQrLogin(expired, { kind: 'checked', result: null })).toEqual({ state: expired, polling: false })
    const failed: QrLoginState = { status: 'failed', message: '', expiresAtMs }
    expect(nextQrLogin(failed, { kind: 'checked', result: { status: 'success', message: '' } })).toEqual({ state: failed, polling: false })
  })

  it('本机判定过期之后才到的登录成功仍按成功处理', () => {
    const expired: QrLoginState = { status: 'expired', message: '', expiresAtMs }
    expect(nextQrLogin(expired, { kind: 'checked', result: { status: 'success', message: '已登录' } }))
      .toEqual({ state: { status: 'success', message: '已登录', expiresAtMs }, polling: false })
  })
})
