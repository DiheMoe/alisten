import type { FormEvent } from 'react'
import { useId, useState } from 'react'
import { api, ApiError, IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { Modal, notificationStore } from '@/shared/ui'
import { loginWithPassword } from './password-login'
import { profileStore, switchIdentity } from './profile-store'

const credentialTexts = { INVALID_ARGUMENT: '用户名要 3–32 位小写字母、数字、_ 或 -，密码要 8–128 位。' }

function submitted(event: FormEvent, run: () => Promise<void>): void {
  event.preventDefault()
  void run()
}

// 绑定和解绑 GitHub 都会更换登录方式，要先输入当前密码，因为只凭这台设备上的登录不能更换。onConfirm 返回 false 时，失败原因已经通过通知显示。
// 绑定成功时页面随即跳到 GitHub，对话框保持忙碌。
export function CurrentPasswordDialog({ title, action, onConfirm, onClose }: { title: string, action: string, onConfirm: (currentPassword: string) => Promise<boolean>, onClose: () => void }) {
  const [currentPassword, setCurrentPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    setBusy(true)
    if (!await onConfirm(currentPassword))
      setBusy(false)
  }
  return (
    <Modal title={title} onClose={onClose}>
      <form className="form-stack" onSubmit={event => submitted(event, submit)}>
        <label>
          当前密码
          <input type="password" data-autofocus required autoComplete="current-password" minLength={8} maxLength={128} value={currentPassword} onChange={event => setCurrentPassword(event.target.value)} />
        </label>
        <button type="submit" className="button-primary" disabled={busy}>{action}</button>
      </form>
    </Modal>
  )
}

// credentials：只绑定了 GitHub 的账号补设用户名和密码，用的是注册接口。
export function CredentialsDialog({ mode, onClose }: { mode: 'register' | 'login' | 'credentials', onClose: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [repeated, setRepeated] = useState('')
  const [busy, setBusy] = useState(false)
  const register = mode !== 'login'
  const submit = async () => {
    setBusy(true)
    try {
      if (register) {
        const { account } = await api.register({ username, password })
        notificationStore.success(mode === 'credentials' ? '已设置用户名和密码，其他设备已退出登录' : '已注册')
        onClose()
        void profileStore.registered(account)
      }
      else {
        switchIdentity(await loginWithPassword(username, password))
      }
    }
    catch (error) {
      // 用 GitHub 确认身份已经过期，在对话框里重试也没用，所以关掉对话框，从按钮重新确认。
      if (mode === 'credentials' && error instanceof ApiError && error.code === 'FORBIDDEN') {
        notificationStore.error('用 GitHub 确认身份已经超过 10 分钟，请在“用户名和密码”一行再点一次“设置”。')
        onClose()
        return
      }
      notificationStore.error(error instanceof IdentityRenewedError ? (register ? '没有注册成功，再试一次。' : '没有登录，再试一次。') : errorMessage(error, credentialTexts))
      setBusy(false)
    }
  }
  return (
    <Modal title={{ register: '注册账号', login: '登录', credentials: '设置用户名和密码' }[mode]} onClose={onClose}>
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
        {mode === 'credentials' && <p className="text-ui-caption muted">设置之后，其他设备需要重新登录。</p>}
        <button type="submit" className="button-primary" disabled={busy || (register && repeated !== password)}>
          {{ register: busy ? '正在注册…' : '注册', login: busy ? '正在登录…' : '登录', credentials: busy ? '正在保存…' : '保存' }[mode]}
        </button>
      </form>
    </Modal>
  )
}

export function PasswordDialog({ onClose }: { onClose: () => void }) {
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
