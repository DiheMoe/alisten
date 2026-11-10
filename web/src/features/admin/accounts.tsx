import type { AdminAccount } from '@/core/realtime'
import { Search } from 'lucide-react'
import { useRef, useState } from 'react'
import { api, ApiError } from '@/core/api'
import { Avatar, ConfirmDialog, notificationStore, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'
import { LoadFailed, SectionHead } from './section-head'

// 新密码只在重置的响应里出现一次，离开这个分类就不再显示。
type Lookup
  = | { phase: 'idle' }
    | { phase: 'searching' }
    | { phase: 'found', account: AdminAccount, password: string | null }
    | { phase: 'missing' }
    | { phase: 'failed', message: string }

async function copyPassword(password: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(password)
    notificationStore.success('已复制新密码')
  }
  catch {
    // 非安全上下文没有剪贴板接口，用户也可能拒绝授权。
    notificationStore.info('没能写入剪贴板，请手动复制新密码。', 8000)
  }
}

function findAccount(username: string): Promise<AdminAccount | null> {
  return adminStore.attempt(() => api.adminGetAccount(username).catch((error: unknown) => {
    if (error instanceof ApiError && error.code === 'NOT_FOUND')
      return null
    throw error
  }))
}

function NewPassword({ password }: { password: string }) {
  return (
    <div className="admin-notice">
      <p>新密码只显示这一次，请转告用户。这个账号在所有设备上都已退出登录。</p>
      <div className="admin-row-actions">
        <code className="admin-code admin-secret">{password}</code>
        <button type="button" className="button-secondary" onClick={() => void copyPassword(password)}>复制</button>
      </div>
    </div>
  )
}

export function Accounts() {
  const [username, setUsername] = useState('')
  const [lookup, setLookup] = useState<Lookup>({ phase: 'idle' })
  const [confirming, setConfirming] = useState(false)
  // 查找进行中又按了回车时，以最后一次为准：先发出的查找晚回来也不覆盖，结果和输入框里的用户名一致。
  const generationRef = useRef(0)
  const find = async () => {
    if (username === '')
      return
    const generation = ++generationRef.current
    setLookup({ phase: 'searching' })
    let next: Lookup
    try {
      const account = await findAccount(username)
      next = account ? { phase: 'found', account, password: null } : { phase: 'missing' }
    }
    catch (error) {
      next = { phase: 'failed', message: (error as Error).message }
    }
    if (generation === generationRef.current)
      setLookup(next)
  }
  return (
    <section className="admin-section" aria-labelledby="admin-users-title">
      <SectionHead id="admin-users-title" title="用户" description="用户忘记密码时，在这里按用户名找到账号，然后重置密码。" />
      <form
        className="admin-toolbar"
        role="search"
        onSubmit={(event) => {
          event.preventDefault()
          void find()
        }}
      >
        <label className="admin-search">
          <Search size={16} aria-hidden="true" />
          <input type="search" enterKeyHint="search" aria-label="用户名" placeholder="输入用户名，按回车查找" autoComplete="off" spellCheck={false} value={username} onChange={event => setUsername(event.target.value.trim().toLowerCase())} />
        </label>
      </form>
      {lookup.phase === 'searching' && <Skeleton rows={1} />}
      {lookup.phase === 'failed' && <LoadFailed message={lookup.message} onRetry={() => void find()} />}
      {lookup.phase === 'missing' && <p className="admin-hint" role="status">没有这个用户名的账号。</p>}
      {lookup.phase === 'found' && (
        <>
          <div className="section-group">
            <div className="section-row">
              <Avatar hash={lookup.account.member.avatarHash} name={lookup.account.member.name} className="admin-user-avatar text-ui-base" />
              <div className="section-row-copy">
                <span className="section-row-title truncate">{lookup.account.member.name}</span>
                <span className="section-row-description">{`@${lookup.account.username}`}</span>
              </div>
              <button type="button" className="button-secondary" onClick={() => setConfirming(true)}>重置密码</button>
            </div>
          </div>
          {lookup.password !== null && <NewPassword password={lookup.password} />}
          {confirming && (
            <ConfirmDialog
              title="重置密码"
              confirmLabel="重置"
              danger
              onConfirm={async () => {
                const { account } = lookup
                const result = await adminStore.call(() => api.adminResetAccountPassword(account.username))
                if (result === null)
                  return
                setLookup({ phase: 'found', account, password: result.password })
                setConfirming(false)
              }}
              onClose={() => setConfirming(false)}
            >
              {`将为 ${lookup.account.username} 生成一个新密码，原来的密码立即失效，这个账号在所有设备上都会退出登录。`}
            </ConfirmDialog>
          )}
        </>
      )}
    </section>
  )
}
