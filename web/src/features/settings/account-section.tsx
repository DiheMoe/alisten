import type { FormEvent } from 'react'
import type { Account, GitHubIntent } from '@/core/realtime'
import { useEffect, useId, useState } from 'react'
import { api, ApiError, IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { useStore } from '@/shared/lib/use-store'
import { Modal, notificationStore } from '@/shared/ui'
import { startGithubLogin, takeCredentialsPrompt } from './github'
import { PairingCodeForm, PairingDialog } from './pairing-dialog'
import { loginWithPassword } from './password-login'
import { profileStore, switchIdentity } from './profile-store'

// credentials：只绑定了 GitHub 的账号补设用户名和密码，用的是注册接口。
type Dialog = 'register' | 'login' | 'credentials' | 'password' | 'pairing' | 'redeem' | null

const credentialTexts = { INVALID_ARGUMENT: '用户名要 3–32 位小写字母、数字、_ 或 -，密码要 8–128 位。' }

// 对话框渲染在 portal 中，React 的事件仍会冒泡到设置页外层的表单，提交时要拦住，否则会顺带保存整页设置。
function submitted(event: FormEvent, run: () => Promise<void>): void {
  event.preventDefault()
  event.stopPropagation()
  void run()
}

// 返回 false 时，失败原因已经通过通知显示；成功时页面随即跳到 GitHub。
async function goToGitHub(intent: GitHubIntent, currentPassword?: string): Promise<boolean> {
  try {
    window.location.assign(await startGithubLogin(intent, currentPassword))
    return true
  }
  catch (error) {
    notificationStore.error(error instanceof IdentityRenewedError ? '没有跳转到 GitHub，再试一次。' : errorMessage(error))
    return false
  }
}

export function AccountRow() {
  const member = useStore(profileStore, s => s.member)
  const account = useStore(profileStore, s => s.account)
  const failed = useStore(profileStore, s => s.failed)
  const [site, setSite] = useState({ registrationOpen: false, githubLogin: false })
  const [dialog, setDialog] = useState<Dialog>(() => takeCredentialsPrompt() ? 'credentials' : null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let active = true
    api.getSite().then(({ registrationOpen, githubLogin }) => {
      if (active)
        setSite({ registrationOpen, githubLogin })
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
  const github = async (intent: GitHubIntent) => {
    setBusy(true)
    if (!await goToGitHub(intent))
      setBusy(false)
  }
  const description = member ? null : failed ? '暂时无法获取你的资料。' : '正在获取你的资料…'
  return (
    <>
      <div className="settings-row">
        <div className="settings-row-copy">
          <p className="settings-row-title text-ui-base">{account ? account.username ?? '用 GitHub 登录的账号' : '游客'}</p>
          {description && <p className="text-ui-caption muted">{description}</p>}
        </div>
        {member && (
          <div className="settings-row-control settings-account-actions">
            {account
              ? (
                  <>
                    <button type="button" className="button-secondary" onClick={() => setDialog('pairing')}>在新设备上登录</button>
                    {account.username
                      ? <button type="button" className="button-secondary" onClick={() => setDialog('password')}>修改密码</button>
                      : <button type="button" className="button-secondary" disabled={busy} onClick={() => void github('verify')}>设置用户名和密码</button>}
                    <button type="button" className="button-secondary" disabled={busy} onClick={() => void logout()}>退出登录</button>
                  </>
                )
              : (
                  <>
                    {site.registrationOpen && <button type="button" className="button-secondary" onClick={() => setDialog('register')}>注册</button>}
                    <button type="button" className="button-secondary" onClick={() => setDialog('login')}>登录</button>
                    {site.githubLogin && <button type="button" className="button-secondary" disabled={busy} onClick={() => void github('login')}>用 GitHub 登录</button>}
                    <button type="button" className="button-secondary" onClick={() => setDialog('redeem')}>输入配对码</button>
                  </>
                )}
          </div>
        )}
        {(dialog === 'register' || dialog === 'login' || dialog === 'credentials') && <CredentialsDialog mode={dialog} onClose={() => setDialog(null)} />}
        {dialog === 'password' && <PasswordDialog onClose={() => setDialog(null)} />}
        {dialog === 'pairing' && <PairingDialog onClose={() => setDialog(null)} />}
        {dialog === 'redeem' && (
          <Modal title="输入配对码" onClose={() => setDialog(null)}>
            <PairingCodeForm onSignedIn={switchIdentity} />
          </Modal>
        )}
      </div>
      {account && site.githubLogin && <GitHubRow account={account} />}
    </>
  )
}

function GitHubRow({ account }: { account: Account }) {
  const [dialog, setDialog] = useState<'bind' | 'unbind' | null>(null)
  const unbind = async (currentPassword: string): Promise<boolean> => {
    try {
      await api.unbindGithub({ currentPassword })
      profileStore.updateAccount({ ...account, github: false })
      notificationStore.success('已解绑 GitHub')
      setDialog(null)
      return true
    }
    catch (error) {
      notificationStore.error(error instanceof IdentityRenewedError ? '没有解绑，再试一次。' : errorMessage(error))
      return false
    }
  }
  const description = !account.github
    ? '绑定之后，可以用 GitHub 登录这个账号。'
    : account.username
      ? '已绑定，可以用 GitHub 登录这个账号。'
      : '已绑定。先设置用户名和密码，才能解绑。'
  return (
    <div className="settings-row">
      <div className="settings-row-copy">
        <p className="settings-row-title text-ui-base">GitHub</p>
        <p className="text-ui-caption muted">{description}</p>
      </div>
      <div className="settings-row-control settings-account-actions">
        {account.github
          ? <button type="button" className="button-secondary" disabled={!account.username} onClick={() => setDialog('unbind')}>解绑</button>
          : <button type="button" className="button-secondary" onClick={() => setDialog('bind')}>绑定 GitHub</button>}
      </div>
      {dialog && (
        <CurrentPasswordDialog
          title={dialog === 'bind' ? '绑定 GitHub' : '解绑 GitHub'}
          action={dialog === 'bind' ? '去 GitHub 授权' : '解绑'}
          onConfirm={dialog === 'bind' ? currentPassword => goToGitHub('bind', currentPassword) : unbind}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  )
}

// 绑定和解绑 GitHub 都会更换登录方式，先输入当前密码：只凭这台设备上的登录不能更换。onConfirm 返回 false 时，失败原因已经通过通知显示；
// 绑定成功时页面随即跳到 GitHub，对话框保持忙碌。
function CurrentPasswordDialog({ title, action, onConfirm, onClose }: { title: string, action: string, onConfirm: (currentPassword: string) => Promise<boolean>, onClose: () => void }) {
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

function CredentialsDialog({ mode, onClose }: { mode: 'register' | 'login' | 'credentials', onClose: () => void }) {
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
      // 用 GitHub 确认身份已经过期：在对话框里重试也没用，关掉它，从按钮重新确认。
      if (mode === 'credentials' && error instanceof ApiError && error.code === 'FORBIDDEN') {
        notificationStore.error('用 GitHub 确认身份已经超过 10 分钟，请再点一次“设置用户名和密码”。')
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
