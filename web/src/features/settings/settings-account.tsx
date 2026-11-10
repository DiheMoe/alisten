import type { Site } from './use-site'
import type { Account, GitHubIntent, Member } from '@/core/realtime'
import { ChevronRight, CircleAlert, Pencil } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { api, IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { isImeKey } from '@/shared/lib/ime'
import { useInlineSave } from '@/shared/lib/use-inline-save'
import { savedNoticeMs } from '@/shared/lib/use-setting-save'
import { useStore } from '@/shared/lib/use-store'
import { Avatar, ConfirmDialog, EmptyState, InlineTextField, Modal, notificationStore, SaveStatus } from '@/shared/ui'
import { CredentialsDialog, CurrentPasswordDialog, PasswordDialog } from './account-dialogs'
import { goToGitHub, takeCredentialsPrompt } from './github'
import { PairingCodeForm, PairingDialog } from './pairing-dialog'
import { profileStore, switchIdentity } from './profile-store'

type Dialog = 'register' | 'login' | 'credentials' | 'password' | 'pairing' | 'redeem' | 'bind' | 'unbind' | 'logout' | null

function identityMeta(account: Account | null): string {
  if (!account)
    return '游客'
  return account.username ? `@${account.username}` : '用 GitHub 登录的账号'
}

// 手机端分类列表顶部的身份卡，是账号分类的入口。
export function IdentityEntry({ onOpen }: { onOpen: () => void }) {
  const member = useStore(profileStore, s => s.member)
  const account = useStore(profileStore, s => s.account)
  const failed = useStore(profileStore, s => s.failed)
  return (
    <button type="button" className="settings-identity-entry" data-section="account" onClick={onOpen}>
      {member
        ? <Avatar hash={member.avatarHash} name={member.name} className="settings-identity-entry-avatar" />
        : <span className="settings-identity-entry-avatar settings-skeleton" />}
      <span className="settings-identity-entry-copy">
        <span className="sr-only">账号：</span>
        {member || failed
          ? <span className="settings-identity-entry-name">{member?.name ?? '账号'}</span>
          : <span className="settings-skeleton settings-skeleton-line" />}
        <span className="settings-identity-entry-meta">{member ? identityMeta(account) : failed ? '资料没有加载出来' : ''}</span>
      </span>
      <ChevronRight className="settings-chevron" size={18} aria-hidden="true" />
    </button>
  )
}

// 编辑时才挂载，这样每次开始编辑都从当前的昵称开始，保存中和失败的状态也随编辑结束一起丢掉。
function NameInput({ name, onDone }: { name: string, onDone: (saved: boolean, refocus: boolean) => void }) {
  const inputRef = useRef<HTMLInputElement>(null)
  // 结束编辑之后输入框随即卸载。有的浏览器会在卸载时触发失焦，这时不能再提交一次。
  const doneRef = useRef(false)
  const errorId = useId()
  const { state, edit, submit } = useInlineSave(name, value => profileStore.save({ name: value.trim() }))

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [])

  const done = (saved: boolean, refocus: boolean) => {
    doneRef.current = true
    onDone(saved, refocus)
  }
  const finish = async (fromKeyboard: boolean) => {
    const changed = state.draft !== state.saved
    if (await submit() && !doneRef.current)
      done(changed, fromKeyboard && document.activeElement === inputRef.current)
  }

  return (
    <>
      <div className="settings-name">
        <input
          ref={inputRef}
          className="settings-name-input"
          value={state.draft}
          aria-label="昵称"
          maxLength={80}
          autoComplete="nickname"
          aria-invalid={state.status === 'error'}
          aria-describedby={state.error ? errorId : undefined}
          aria-busy={state.status === 'saving'}
          onChange={event => edit(event.target.value)}
          onBlur={() => {
            if (!doneRef.current)
              void finish(false)
          }}
          onKeyDown={(event) => {
            if (isImeKey(event.nativeEvent))
              return
            if (event.key === 'Enter') {
              event.preventDefault()
              void finish(true)
            }
            // 已经发出的保存撤不回来，等它的结果。
            else if (event.key === 'Escape' && state.status !== 'saving') {
              event.preventDefault()
              done(false, true)
            }
          }}
        />
        <SaveStatus status={state.status === 'saving' ? 'saving' : 'idle'} />
      </div>
      {state.error && <p id={errorId} className="settings-row-error text-ui-caption" role="alert">{state.error}</p>}
    </>
  )
}

function NameEditor({ name }: { name: string }) {
  const [editing, setEditing] = useState(false)
  const [notice, setNotice] = useState(0)
  const pencilRef = useRef<HTMLButtonElement>(null)
  const refocusRef = useRef(false)

  useEffect(() => {
    if (editing || !refocusRef.current)
      return
    refocusRef.current = false
    pencilRef.current?.focus()
  }, [editing])

  useEffect(() => {
    if (!notice)
      return
    const timer = setTimeout(setNotice, savedNoticeMs, 0)
    return () => clearTimeout(timer)
  }, [notice])

  if (editing) {
    return (
      <NameInput
        name={name}
        onDone={(saved, refocus) => {
          refocusRef.current = refocus
          setEditing(false)
          setNotice(count => saved ? count + 1 : 0)
        }}
      />
    )
  }
  return (
    <div className="settings-name">
      {/* 点名字也能编辑。键盘操作时用旁边的铅笔按钮。 */}
      <span className="settings-name-text" onClick={() => setEditing(true)}>{name}</span>
      <button ref={pencilRef} type="button" className="icon-button settings-name-edit" aria-label="编辑昵称" title="编辑昵称" onClick={() => setEditing(true)}>
        <Pencil size={15} strokeWidth={1.9} aria-hidden="true" />
      </button>
      <SaveStatus status={notice ? 'saved' : 'idle'} version={notice} />
    </div>
  )
}

function IdentityCard({ member, account }: { member: Member, account: Account | null }) {
  return (
    <div className="settings-identity">
      {/* 有头像时，用头像模糊后的样子做光晕。没有头像时这一层只有首字，由 CSS 隐藏。 */}
      <Avatar hash={member.avatarHash} name={member.name} className="settings-identity-glow" />
      <Avatar hash={member.avatarHash} name={member.name} className="settings-identity-avatar" />
      <div className="settings-identity-copy">
        <NameEditor name={member.name} />
        <p className="settings-identity-meta">{identityMeta(account)}</p>
        {!account && <p className="settings-identity-meta">注册之后可以在其他设备上登录</p>}
        {account?.github && <span className="settings-badge">GitHub 已绑定</span>}
      </div>
    </div>
  )
}

function AccountSkeleton() {
  return (
    <div role="status" aria-label="正在加载">
      <div className="settings-identity">
        <span className="settings-identity-avatar settings-skeleton" />
        <div className="settings-identity-copy">
          <span className="settings-skeleton settings-skeleton-line" />
          <span className="settings-skeleton settings-skeleton-line settings-skeleton-short" />
        </div>
      </div>
      <p className="section-group-label"><span className="settings-skeleton settings-skeleton-label" /></p>
      <div className="section-group settings-skeleton-group" />
    </div>
  )
}

export function AccountSettings({ site, onRetrySite }: { site: Site, onRetrySite: () => void }) {
  const member = useStore(profileStore, s => s.member)
  const account = useStore(profileStore, s => s.account)
  const email = useStore(profileStore, s => s.email)
  const failed = useStore(profileStore, s => s.failed)
  const [dialog, setDialog] = useState<Dialog>(() => takeCredentialsPrompt() ? 'credentials' : null)
  const [busy, setBusy] = useState(false)
  const close = () => setDialog(null)

  const github = async (intent: GitHubIntent) => {
    setBusy(true)
    if (!await goToGitHub(intent))
      setBusy(false)
  }
  const unbind = async (currentPassword: string): Promise<boolean> => {
    if (!account)
      return false
    try {
      await api.unbindGithub({ currentPassword })
      profileStore.updateAccount({ ...account, github: false })
      notificationStore.success('已解绑 GitHub')
      close()
      return true
    }
    catch (error) {
      notificationStore.error(error instanceof IdentityRenewedError ? '没有解绑，再试一次。' : errorMessage(error))
      return false
    }
  }

  const loadFailed = (!member && failed) || site.status === 'failed'
  const retry = () => {
    if (!member && failed)
      void profileStore.load().catch(() => {})
    if (site.status === 'failed')
      onRetrySite()
  }

  let content
  if (loadFailed) {
    content = (
      <EmptyState icon={CircleAlert} title="账号信息没有加载出来" action={<button type="button" className="button-secondary" onClick={retry}>重试</button>}>
        可能是网络中断，检查网络后再试。
      </EmptyState>
    )
  }
  else if (!member || site.status !== 'ready') {
    content = <AccountSkeleton />
  }
  else {
    content = (
      <>
        <IdentityCard member={member} account={account} />
        {!account && (
          <div className="settings-guest-actions">
            {site.registrationOpen && <button type="button" className="button-primary" onClick={() => setDialog('register')}>注册</button>}
            <button type="button" className={site.registrationOpen ? 'button-secondary' : 'button-primary'} onClick={() => setDialog('login')}>登录</button>
            {site.githubLogin && <button type="button" className="button-secondary" disabled={busy} onClick={() => void github('login')}>用 GitHub 登录</button>}
            <button type="button" className="button-secondary" onClick={() => setDialog('redeem')}>输入配对码</button>
          </div>
        )}

        <p className="section-group-label">资料</p>
        <div className="section-group">
          <div className="section-row">
            <div className="section-row-copy">
              <span className="section-row-title">邮箱</span>
              <p className="section-row-description">{account ? '用来显示头像，各设备一致' : '用来显示头像，只保存在这台设备上'}</p>
            </div>
            <InlineTextField label="邮箱" type="email" value={email} placeholder="name@example.com" maxLength={320} onSave={value => profileStore.save({ email: value.trim() })} />
          </div>
        </div>

        {account && (
          <>
            <p className="section-group-label">登录方式</p>
            <div className="section-group">
              <div className="section-row">
                <div className="section-row-copy">
                  <span className="section-row-title">{account.username ? '密码' : '用户名和密码'}</span>
                  <p className="section-row-description">{account.username ? '修改后，其他设备需要重新登录' : '设置之后可以用密码登录'}</p>
                </div>
                {account.username
                  ? <button type="button" className="button-secondary settings-button" onClick={() => setDialog('password')}>修改</button>
                  : <button type="button" className="button-secondary settings-button" disabled={busy} onClick={() => void github('verify')}>设置</button>}
              </div>
              {site.githubLogin && (
                <div className="section-row">
                  <div className="section-row-copy">
                    <span className="section-row-title">GitHub</span>
                    <p className="section-row-description">
                      {!account.github ? '绑定之后可以用 GitHub 登录这个账号' : account.username ? '可以用 GitHub 登录这个账号' : '先设置用户名和密码，才能解绑'}
                    </p>
                  </div>
                  <div className="settings-row-control">
                    {account.github && <span className="settings-badge">已绑定</span>}
                    {account.github
                      ? <button type="button" className="button-secondary settings-button" disabled={!account.username} onClick={() => setDialog('unbind')}>解绑</button>
                      : <button type="button" className="button-secondary settings-button" onClick={() => setDialog('bind')}>绑定</button>}
                  </div>
                </div>
              )}
            </div>

            <p className="section-group-label">设备</p>
            <div className="section-group">
              <button type="button" className="section-row" onClick={() => setDialog('pairing')}>
                <span className="section-row-copy">
                  <span className="section-row-title">在新设备上登录</span>
                  <span className="section-row-description">显示配对码和二维码</span>
                </span>
                <ChevronRight className="settings-chevron" size={18} aria-hidden="true" />
              </button>
              <button type="button" className="section-row" onClick={() => setDialog('logout')}>
                <span className="section-row-copy">
                  <span className="section-row-title settings-danger">退出登录</span>
                </span>
              </button>
            </div>
          </>
        )}
      </>
    )
  }

  return (
    <>
      <h2 className="settings-title">账号</h2>
      {content}
      {(dialog === 'register' || dialog === 'login' || dialog === 'credentials') && <CredentialsDialog mode={dialog} onClose={close} />}
      {dialog === 'password' && <PasswordDialog onClose={close} />}
      {dialog === 'pairing' && <PairingDialog onClose={close} />}
      {dialog === 'redeem' && (
        <Modal title="输入配对码" onClose={close}>
          <PairingCodeForm onSignedIn={switchIdentity} />
        </Modal>
      )}
      {(dialog === 'bind' || dialog === 'unbind') && (
        <CurrentPasswordDialog
          title={dialog === 'bind' ? '绑定 GitHub' : '解绑 GitHub'}
          action={dialog === 'bind' ? '去 GitHub 授权' : '解绑'}
          onConfirm={dialog === 'bind' ? currentPassword => goToGitHub('bind', currentPassword) : unbind}
          onClose={close}
        />
      )}
      {dialog === 'logout' && (
        <ConfirmDialog
          title="退出登录"
          confirmLabel="退出登录"
          danger
          onConfirm={async () => {
            await profileStore.logout()
          }}
          onClose={close}
        >
          退出之后，这台设备会换成新的游客身份。
        </ConfirmDialog>
      )}
    </>
  )
}
