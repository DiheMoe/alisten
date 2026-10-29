import type { QRLoginResult, QRLoginStatus } from '@/core/realtime'

export const pollIntervalMs = 2000

export interface QrLoginState {
  status: QRLoginStatus
  message: string
  expiresAtMs: number
}

// result 为 null 表示这次查询失败，adminStore 已经提示了原因。
export type QrLoginEvent
  = | { kind: 'checked', result: QRLoginResult | null }
    | { kind: 'tick', nowMs: number }

export interface QrLoginStep {
  state: QrLoginState
  polling: boolean
}

export function startQrLogin(expiresAtMs: number): QrLoginState {
  return { status: 'waiting', message: '', expiresAtMs }
}

function pending(status: QRLoginStatus): boolean {
  return status === 'waiting' || status === 'scanned'
}

export function nextQrLogin(state: QrLoginState, event: QrLoginEvent): QrLoginStep {
  if (!pending(state.status)) {
    // 本机判定过期时可能还有一次查询尚未返回：它返回 success 时服务端已经保存了登录状态，仍按成功处理。
    if (state.status === 'expired' && event.kind === 'checked' && event.result?.status === 'success')
      return { state: { ...state, status: 'success', message: event.result.message }, polling: false }
    return { state, polling: false }
  }
  if (event.kind === 'tick') {
    if (event.nowMs < state.expiresAtMs)
      return { state, polling: true }
    return { state: { ...state, status: 'expired', message: '' }, polling: false }
  }
  if (event.result === null)
    return { state: { ...state, status: 'failed', message: '' }, polling: false }
  const next = { ...state, status: event.result.status, message: event.result.message }
  return { state: next, polling: pending(next.status) }
}

export function remainingSeconds(state: QrLoginState, nowMs: number): number {
  return Math.max(0, Math.ceil((state.expiresAtMs - nowMs) / 1000))
}
