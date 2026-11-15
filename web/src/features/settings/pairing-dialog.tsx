import { useEffect, useState } from 'react'
import { notificationStore } from '@/shared/lib/notification-store'
import { useResource } from '@/shared/lib/use-resource'
import { Modal } from '@/shared/ui'
import { checkPairingCode, createPairingCode, redeemPairingCode } from './account'
import { countdownText, formatPairingCode, normalizePairingCode, secondsLeft } from './pairing'

// 已登录的设备生成配对码，新设备输入它或者扫描二维码登录同一个账号。
export function PairingDialog({ onClose }: { onClose: () => void }) {
  const [nowMs, setNowMs] = useState(Date.now)
  const { state: pairing, reload: retry } = useResource(() => createPairingCode().then((created) => {
    setNowMs(Date.now())
    return created
  }))
  useEffect(() => {
    if (pairing.status === 'failed')
      notificationStore.error(pairing.error)
  }, [pairing])
  useEffect(() => {
    const tick = setInterval(() => setNowMs(Date.now()), 1000)
    return () => clearInterval(tick)
  }, [])
  const seconds = pairing.status === 'loaded' ? secondsLeft(pairing.value.expiresAtMs, nowMs) : 0
  return (
    <Modal title="在新设备上登录" onClose={onClose}>
      <div className="settings-pairing">
        {pairing.status === 'loading' && <p className="text-ui-base muted">正在生成配对码…</p>}
        {pairing.status === 'failed' && <p className="text-ui-base muted">暂时无法生成配对码。</p>}
        {pairing.status === 'loaded' && seconds > 0 && (
          <>
            <img className="settings-pairing-qr" src={pairing.value.qrImage} alt="配对二维码" />
            {/* 读屏软件逐个字符读出配对码，不把可见的 XXXX-XXXX 当作单词来读。 */}
            <p className="sr-only">{`配对码 ${pairing.value.code.split('').join(' ')}`}</p>
            <p className="settings-pairing-code text-ui-xl" aria-hidden="true">{formatPairingCode(pairing.value.code)}</p>
            <p className="text-ui-caption muted">在新设备上扫描二维码，或者打开设置里的“输入配对码”。配对码只能用一次。</p>
            <span className="text-ui-sm muted" role="timer">{`${countdownText(seconds)} 后失效`}</span>
          </>
        )}
        {pairing.status === 'loaded' && seconds === 0 && <p className="text-ui-base muted" role="status">配对码已经失效。</p>}
        {(pairing.status === 'failed' || (pairing.status === 'loaded' && seconds === 0)) && (
          <button type="button" className="button-primary" onClick={retry}>重新生成</button>
        )}
      </div>
    </Modal>
  )
}

// 兑换配对码就是登录。成功时把新会话的成员 token 交给调用方，由调用方换身份。
export function PairingCodeForm({ initialCode = '', onSignedIn }: { initialCode?: string, onSignedIn: (memberToken: string) => void }) {
  const [input, setInput] = useState(() => normalizePairingCode(initialCode))
  const [busy, setBusy] = useState(false)
  const code = normalizePairingCode(input)
  const submit = async () => {
    if (!checkPairingCode(code))
      return
    setBusy(true)
    const memberToken = await redeemPairingCode(code)
    if (memberToken === null)
      setBusy(false)
    else
      onSignedIn(memberToken)
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
