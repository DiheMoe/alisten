import type { QrLoginEvent, QrLoginState } from './qr-login'
import type { QRLogin, QRLoginResult, QRLoginStatus } from '@/core/realtime'
import { useEffect, useEffectEvent, useState } from 'react'
import { Modal } from '@/shared/ui'
import { adminStore } from './admin-store'
import { nextQrLogin, pollIntervalMs, remainingSeconds, startQrLogin } from './qr-login'

export interface QrLoginTarget {
  // 有多种扫码方式时显示在切换按钮上。
  label: string
  start: () => Promise<QRLogin>
  check: (key: string) => Promise<QRLoginResult>
}

const statusText: Record<QRLoginStatus, string> = {
  waiting: '等待扫码',
  scanned: '已扫码，请在手机上确认',
  success: '登录成功',
  expired: '二维码已过期',
  failed: '登录失败',
}

export function QrLoginDialog({ title, targets, onSuccess, onClose }: {
  title: string
  targets: QrLoginTarget[]
  onSuccess: () => void
  onClose: () => void
}) {
  const [selected, setSelected] = useState(0)
  const [qr, setQr] = useState<QRLogin | 'loading' | 'unavailable'>('loading')
  const [login, setLogin] = useState<QrLoginState | null>(null)
  const [nowMs, setNowMs] = useState(Date.now)
  const [attempt, setAttempt] = useState(0)
  const succeeded = useEffectEvent(onSuccess)
  const target = targets[selected]
  useEffect(() => {
    let active = true
    let state: QrLoginState | null = null
    let poll: ReturnType<typeof setTimeout> | undefined
    let tick: ReturnType<typeof setInterval> | undefined
    const apply = (event: QrLoginEvent): boolean => {
      if (!active || !state)
        return false
      const step = nextQrLogin(state, event)
      state = step.state
      setLogin(state)
      if (!step.polling) {
        clearTimeout(poll)
        clearInterval(tick)
        if (state.status === 'success')
          succeeded()
      }
      return step.polling
    }
    const check = async (key: string) => {
      const result = await adminStore.call(() => target.check(key))
      if (apply({ kind: 'checked', result }))
        poll = setTimeout(() => void check(key), pollIntervalMs)
    }
    void adminStore.call(target.start).then((started) => {
      if (!active)
        return
      if (!started) {
        setQr('unavailable')
        return
      }
      state = startQrLogin(started.expiresAtMs)
      setQr(started)
      setLogin(state)
      setNowMs(Date.now())
      tick = setInterval(() => {
        const now = Date.now()
        setNowMs(now)
        apply({ kind: 'tick', nowMs: now })
      }, 1000)
      poll = setTimeout(() => void check(started.key), pollIntervalMs)
    })
    return () => {
      active = false
      clearTimeout(poll)
      clearInterval(tick)
    }
  }, [target, attempt])
  const restart = () => {
    setQr('loading')
    setLogin(null)
  }
  const retry = () => {
    restart()
    setAttempt(n => n + 1)
  }
  const scanning = login?.status === 'waiting' || login?.status === 'scanned'
  return (
    <Modal title={title} onClose={onClose}>
      <div className="admin-qr">
        {targets.length > 1 && (
          <div className="admin-qr-switch" role="group" aria-label="扫码方式">
            {targets.map((option, index) => (
              <button
                type="button"
                key={option.label}
                className="text-ui-caption"
                aria-pressed={index === selected}
                onClick={() => {
                  if (index === selected)
                    return
                  restart()
                  setSelected(index)
                }}
              >
                {option.label}
              </button>
            ))}
          </div>
        )}
        {qr === 'loading' && <p className="text-ui-base muted">正在获取二维码…</p>}
        {qr === 'unavailable' && <p className="text-ui-base muted">暂时无法获取二维码。</p>}
        {typeof qr === 'object' && login && (
          <>
            {scanning && <img className="admin-qr-image" src={qr.qrImage} alt="登录二维码" referrerPolicy="no-referrer" />}
            <p className="text-ui-base" role="status">
              {login.status === 'failed' && login.message ? `${statusText.failed}：${login.message}` : statusText[login.status]}
            </p>
            {scanning && <span className="text-ui-sm muted">{`剩余 ${remainingSeconds(login, nowMs)} 秒`}</span>}
          </>
        )}
        {(qr === 'unavailable' || login?.status === 'expired' || login?.status === 'failed') && (
          <button type="button" className="button-primary" onClick={retry}>重新获取</button>
        )}
      </div>
    </Modal>
  )
}
