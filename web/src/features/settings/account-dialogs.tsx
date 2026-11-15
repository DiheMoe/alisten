import type { FormEvent } from 'react'
import type { CredentialsMode } from './account'
import { useId, useState } from 'react'
import { AccountPasswordLimits } from '@/core/realtime'
import { useSubmission } from '@/shared/lib/use-submission'
import { Modal } from '@/shared/ui'
import { changePassword, submitCredentials } from './account'

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
          <input type="password" data-autofocus required autoComplete="current-password" minLength={AccountPasswordLimits.minLength} maxLength={AccountPasswordLimits.maxLength} value={currentPassword} onChange={event => setCurrentPassword(event.target.value)} />
        </label>
        <button type="submit" className="button-primary" disabled={busy}>{action}</button>
      </form>
    </Modal>
  )
}

export function CredentialsDialog({ mode, onClose }: { mode: CredentialsMode, onClose: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [repeated, setRepeated] = useState('')
  const [busy, setBusy] = useState(false)
  const register = mode !== 'login'
  const submit = async () => {
    setBusy(true)
    const next = await submitCredentials(mode, username, password)
    if (next === 'close')
      onClose()
    else if (next === 'retry')
      setBusy(false)
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
          <input type="password" required autoComplete={register ? 'new-password' : 'current-password'} minLength={AccountPasswordLimits.minLength} maxLength={AccountPasswordLimits.maxLength} value={password} onChange={event => setPassword(event.target.value)} />
        </label>
        {register && <RepeatPassword label="再次输入密码" password={password} value={repeated} onChange={setRepeated} />}
        <p className="text-ui-caption muted">
          {register
            ? `用户名 3–32 位，可用小写字母、数字、_ 和 -；密码至少 ${AccountPasswordLimits.minLength} 位。忘记密码需联系管理员。`
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
  const { busy, submit } = useSubmission()
  const change = async () => {
    if (await changePassword(currentPassword, newPassword))
      onClose()
  }
  return (
    <Modal title="修改密码" onClose={onClose}>
      <form className="form-stack" onSubmit={event => submitted(event, () => submit(change))}>
        <label>
          当前密码
          <input type="password" data-autofocus required autoComplete="current-password" minLength={AccountPasswordLimits.minLength} maxLength={AccountPasswordLimits.maxLength} value={currentPassword} onChange={event => setCurrentPassword(event.target.value)} />
        </label>
        <label>
          新密码
          <input type="password" required autoComplete="new-password" minLength={AccountPasswordLimits.minLength} maxLength={AccountPasswordLimits.maxLength} value={newPassword} onChange={event => setNewPassword(event.target.value)} />
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
        <input type="password" required autoComplete="new-password" minLength={AccountPasswordLimits.minLength} maxLength={AccountPasswordLimits.maxLength} value={value} onChange={event => onChange(event.target.value)} aria-invalid={mismatched} aria-describedby={mismatched ? hintId : undefined} />
      </label>
      {mismatched && <p id={hintId} className="text-ui-caption error-text">两次输入的密码不一样。</p>}
    </>
  )
}
