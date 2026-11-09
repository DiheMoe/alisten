import type { AdminAccount } from '@/core/realtime'
import { useState } from 'react'
import { api } from '@/core/api'
import { ConfirmDialog } from '@/shared/ui'
import { adminStore } from './admin-store'

export function Accounts() {
  const [username, setUsername] = useState('')
  const [found, setFound] = useState<AdminAccount | null>(null)
  const [password, setPassword] = useState<string | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const find = async () => {
    setBusy(true)
    try {
      setFound(await adminStore.call(() => api.adminGetAccount(username), { NOT_FOUND: '没有这个用户名的账号。' }))
      setPassword(null)
    }
    finally {
      setBusy(false)
    }
  }
  const reset = async (account: AdminAccount) => {
    const result = await adminStore.call(() => api.adminResetAccountPassword(account.username))
    if (result !== null)
      setPassword(result.password)
    setConfirming(false)
  }
  return (
    <section className="admin-section" aria-labelledby="admin-accounts-title">
      <div className="section-heading">
        <h2 id="admin-accounts-title" className="text-ui-lg">账号</h2>
      </div>
      <form
        className="admin-list"
        onSubmit={(event) => {
          event.preventDefault()
          void find()
        }}
      >
        <div className="admin-row">
          <div className="admin-row-copy">
            <label htmlFor="admin-account-username" className="admin-row-title text-ui-base">查找账号</label>
            <span id="admin-account-username-description" className="text-ui-sm muted">按用户名查找。用户忘记密码时，可以在这里重置。</span>
          </div>
          <div className="admin-row-actions">
            <input id="admin-account-username" aria-describedby="admin-account-username-description" autoComplete="off" value={username} onChange={event => setUsername(event.target.value.trim().toLowerCase())} />
            <button type="submit" className="button-secondary" disabled={busy || username === ''}>查找</button>
          </div>
        </div>
        {found && (
          <div className="admin-row">
            <div className="admin-row-copy">
              <span className="admin-row-title text-ui-base">{found.username}</span>
              <span className="text-ui-sm muted">{`昵称：${found.member.name}`}</span>
            </div>
            <div className="admin-row-actions">
              <button type="button" className="button-secondary" onClick={() => setConfirming(true)}>重置密码</button>
            </div>
          </div>
        )}
        {found && password && (
          <div className="admin-row">
            <div className="admin-row-copy">
              <span className="admin-row-title text-ui-base">
                新密码：
                <code>{password}</code>
              </span>
              <span className="text-ui-sm muted">只显示这一次，请转告用户。这个账号在所有设备上都已退出登录。</span>
            </div>
          </div>
        )}
      </form>
      {found && confirming && (
        <ConfirmDialog title="重置密码" confirmLabel="重置" danger onConfirm={() => reset(found)} onClose={() => setConfirming(false)}>
          {`将为 ${found.username} 生成一个新密码，原来的密码立即失效，这个账号在所有设备上都会退出登录。`}
        </ConfirmDialog>
      )}
    </section>
  )
}
