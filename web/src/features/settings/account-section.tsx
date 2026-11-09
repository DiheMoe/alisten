import type { FormEvent } from 'react'
import { useEffect, useId, useState } from 'react'
import { api, IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { useStore } from '@/shared/lib/use-store'
import { Modal, notificationStore } from '@/shared/ui'
import { loginWithPassword } from './password-login'
import { profileStore, switchIdentity } from './profile-store'

type Dialog = 'register' | 'login' | 'password' | null

const credentialTexts = { INVALID_ARGUMENT: '用户名要 3–32 位小写字母、数字、_ 或 -，密码要 8–128 位。' }

// 对话框渲染在 portal 中，React 的事件仍会冒泡到设置页外层的表单，提交时要拦住，否则会顺带保存整页设置。
function submitted(event: FormEvent, run: () => Promise<void>): void {
  event.preventDefault()
  event.stopPropagation()
  void run()
}

export function AccountRow() {
  const member = useStore(profileStore, s => s.member)
  const account = useStore(profileStore, s => s.account)
  const failed = useStore(profileStore, s => s.failed)
  const [registrationOpen, setRegistrationOpen] = useState(false)
  const [dialog, setDialog] = useState<Dialog>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let active = true
    api.getSite().then((site) => {
      if (active)
        setRegistrationOpen(site.registrationOpen)
    }, () => {})
    return () => {
      active = false
    }
  }, [])
  const logout = async () => {
    setBusy(true)
    if (!await profileStore.logout())
      setBusy(false)
  }
  const description = member ? null : failed ? '暂时无法获取你的资料。' : '正在获取你的资料…'
  return (
    <div className="settings-row">
      <div className="settings-row-copy">
        <p className="settings-row-title text-ui-base">{account ? account.username : '游客'}</p>
        {description && <p className="text-ui-caption muted">{description}</p>}
      </div>
      {member && (
        <div className="settings-row-control settings-account-actions">
          {account
            ? (
                <>
                  <button type="button" className="button-secondary" onClick={() => setDialog('password')}>修改密码</button>
                  <button type="button" className="button-secondary" disabled={busy} onClick={() => void logout()}>退出登录</button>
                </>
              )
            : (
                <>
                  {registrationOpen && <button type="button" className="button-secondary" onClick={() => setDialog('register')}>注册</button>}
                  <button type="button" className="button-secondary" onClick={() => setDialog('login')}>登录</button>
                </>
              )}
        </div>
      )}
      {(dialog === 'register' || dialog === 'login') && <CredentialsDialog mode={dialog} onClose={() => setDialog(null)} />}
      {dialog === 'password' && <PasswordDialog onClose={() => setDialog(null)} />}
    </div>
  )
}

function CredentialsDialog({ mode, onClose }: { mode: 'register' | 'login', onClose: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [repeated, setRepeated] = useState('')
  const [busy, setBusy] = useState(false)
  const register = mode === 'register'
  const submit = async () => {
    setBusy(true)
    try {
      if (register) {
        const { account } = await api.register({ username, password })
        notificationStore.success('已注册')
        onClose()
        void profileStore.registered(account)
      }
      else {
        switchIdentity(await loginWithPassword(username, password))
      }
    }
    catch (error) {
      notificationStore.error(error instanceof IdentityRenewedError ? (register ? '没有注册成功，再试一次。' : '没有登录，再试一次。') : errorMessage(error, credentialTexts))
      setBusy(false)
    }
  }
  return (
    <Modal title={register ? '注册账号' : '登录'} onClose={onClose}>
      <form className="form-stack" onSubmit={event => submitted(event, submit)}>
        <label>
          用户名
          <input data-autofocus required autoComplete="username" pattern="[a-z0-9_\-]{3,32}" minLength={3} maxLength={32} value={username} onChange={event => setUsername(event.target.value.toLowerCase())} />
        </label>
        <label>
          密码
          <input type="password" required autoComplete={register ? 'new-password' : 'current-password'} minLength={8} maxLength={128} value={password} onChange={event => setPassword(event.target.value)} />
        </label>
        {register && <RepeatPassword label="再次输入密码" password={password} value={repeated} onChange={setRepeated} />}
        <p className="text-ui-caption muted">
          {register
            ? '用户名 3–32 位，可用小写字母、数字、_ 和 -；密码至少 8 位。忘记密码需联系管理员。'
            : '登录后页面会重新加载。'}
        </p>
        <button type="submit" className="button-primary" disabled={busy || (register && repeated !== password)}>
          {busy ? (register ? '正在注册…' : '正在登录…') : (register ? '注册' : '登录')}
        </button>
      </form>
    </Modal>
  )
}

function PasswordDialog({ onClose }: { onClose: () => void }) {
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [repeated, setRepeated] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    setBusy(true)
    try {
      await api.changePassword({ currentPassword, newPassword })
      notificationStore.success('已修改密码，其他设备已退出登录')
      onClose()
    }
    catch (error) {
      notificationStore.error(error instanceof IdentityRenewedError ? '密码没有修改，再试一次。' : errorMessage(error, { INVALID_ARGUMENT: '密码要 8 到 128 位。' }))
      setBusy(false)
    }
  }
  return (
    <Modal title="修改密码" onClose={onClose}>
      <form className="form-stack" onSubmit={event => submitted(event, submit)}>
        <label>
          当前密码
          <input type="password" data-autofocus required autoComplete="current-password" minLength={8} maxLength={128} value={currentPassword} onChange={event => setCurrentPassword(event.target.value)} />
        </label>
        <label>
          新密码
          <input type="password" required autoComplete="new-password" minLength={8} maxLength={128} value={newPassword} onChange={event => setNewPassword(event.target.value)} />
        </label>
        <RepeatPassword label="再次输入新密码" password={newPassword} value={repeated} onChange={setRepeated} />
        <p className="text-ui-caption muted">修改后，其他设备需要重新登录。</p>
        <button type="submit" className="button-primary" disabled={busy || repeated !== newPassword}>{busy ? '正在修改…' : '修改密码'}</button>
      </form>
    </Modal>
  )
}

function RepeatPassword({ label, password, value, onChange }: { label: string, password: string, value: string, onChange: (value: string) => void }) {
  const hintId = useId()
  const mismatched = value !== '' && value !== password
  return (
    <>
      <label>
        {label}
        <input type="password" required autoComplete="new-password" minLength={8} maxLength={128} value={value} onChange={event => onChange(event.target.value)} aria-invalid={mismatched} aria-describedby={mismatched ? hintId : undefined} />
      </label>
      {mismatched && <p id={hintId} className="text-ui-caption error-text">两次输入的密码不一样。</p>}
    </>
  )
}
