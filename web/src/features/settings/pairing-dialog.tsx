import type { CreatePairingCodeResponse } from '@/core/api'
import { useEffect, useState } from 'react'
import { api, IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { Modal, notificationStore } from '@/shared/ui'
import { countdownText, formatPairingCode, isPairingCode, normalizePairingCode, secondsLeft } from './pairing'

// 已登录的设备生成配对码，新设备输入它或者扫描二维码登录同一个账号。
export function PairingDialog({ onClose }: { onClose: () => void }) {
  const [pairing, setPairing] = useState<CreatePairingCodeResponse | 'loading' | 'failed'>('loading')
  const [attempt, setAttempt] = useState(0)
  const [nowMs, setNowMs] = useState(Date.now)
  useEffect(() => {
    let active = true
    api.createPairingCode().then((created) => {
      if (!active)
        return
      setPairing(created)
      setNowMs(Date.now())
    }, (error: unknown) => {
      if (!active)
        return
      setPairing('failed')
      notificationStore.error(error instanceof IdentityRenewedError ? '没有生成配对码，再试一次。' : errorMessage(error))
    })
    const tick = setInterval(() => setNowMs(Date.now()), 1000)
    return () => {
      active = false
      clearInterval(tick)
    }
  }, [attempt])
  const retry = () => {
    setPairing('loading')
    setAttempt(n => n + 1)
  }
  const seconds = typeof pairing === 'object' ? secondsLeft(pairing.expiresAtMs, nowMs) : 0
  return (
    <Modal title="在新设备上登录" onClose={onClose}>
      <div className="settings-pairing">
        {pairing === 'loading' && <p className="text-ui-base muted">正在生成配对码…</p>}
        {pairing === 'failed' && <p className="text-ui-base muted">暂时无法生成配对码。</p>}
        {typeof pairing === 'object' && seconds > 0 && (
          <>
            <img className="settings-pairing-qr" src={pairing.qrImage} alt="配对二维码" />
            {/* 读屏软件逐个字符读出配对码，不把可见的 XXXX-XXXX 当作单词来读。 */}
            <p className="sr-only">{`配对码 ${pairing.code.split('').join(' ')}`}</p>
            <p className="settings-pairing-code text-ui-xl" aria-hidden="true">{formatPairingCode(pairing.code)}</p>
            <p className="text-ui-caption muted">在新设备上扫描二维码，或者打开设置里的“输入配对码”。配对码只能用一次。</p>
            <span className="text-ui-sm muted" role="timer">{`${countdownText(seconds)} 后失效`}</span>
          </>
        )}
        {typeof pairing === 'object' && seconds === 0 && <p className="text-ui-base muted" role="status">配对码已经失效。</p>}
        {(pairing === 'failed' || (typeof pairing === 'object' && seconds === 0)) && (
          <button type="button" className="button-primary" onClick={retry}>重新生成</button>
        )}
      </div>
    </Modal>
  )
}

// 兑换配对码就是登录：成功时把新会话的成员 token 交给调用方，由它换身份。
export function PairingCodeForm({ initialCode = '', onSignedIn }: { initialCode?: string, onSignedIn: (memberToken: string) => void }) {
  const [input, setInput] = useState(() => normalizePairingCode(initialCode))
  const [busy, setBusy] = useState(false)
  const code = normalizePairingCode(input)
  const submit = async () => {
    if (!isPairingCode(code)) {
      notificationStore.error('配对码是 8 位字母和数字，不含 0、O、1、I。')
      return
    }
    setBusy(true)
    try {
      const { memberToken } = await api.redeemPairingCode({ code })
      onSignedIn(memberToken)
    }
    catch (error) {
      notificationStore.error(error instanceof IdentityRenewedError ? '没有登录，再试一次。' : errorMessage(error, { INVALID_ARGUMENT: '配对码是 8 位字母和数字，不含 0、O、1、I。' }))
      setBusy(false)
    }
  }
  return (
    <form
      className="form-stack"
      onSubmit={(event) => {
        // 对话框渲染在 portal 中，React 的事件仍会冒泡到设置页外层的表单。
        event.preventDefault()
        event.stopPropagation()
        void submit()
      }}
    >
      <label>
        配对码
        <input data-autofocus required autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} maxLength={16} value={input} onChange={event => setInput(event.target.value)} />
      </label>
      <p className="text-ui-caption muted">在已登录的设备上打开设置，点“在新设备上登录”就能看到配对码。只输入你自己生成的配对码：登录之后，这台设备上的操作都属于那个账号。</p>
      <button type="submit" className="button-primary" disabled={busy}>{busy ? '正在登录…' : '登录'}</button>
    </form>
  )
}
