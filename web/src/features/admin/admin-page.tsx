import { LogOut } from 'lucide-react'
import { useState } from 'react'
import { brandStore } from '@/core/brand'
import { useStore } from '@/shared/lib/use-store'
import { adminStore } from './admin-store'
import { PersistentRooms } from './persistent-rooms'
import { SiteSettings } from './site-settings'
import { SourceAccounts } from './source-accounts'
import { StoredMedia } from './stored-media'
import './admin.css'

function SignIn() {
  const [token, setToken] = useState('')
  return (
    <form
      className="form-stack admin-sign-in"
      onSubmit={(event) => {
        event.preventDefault()
        adminStore.signIn(token)
      }}
    >
      <label>
        管理 token
        <input type="password" autoComplete="off" autoFocus required value={token} onChange={event => setToken(event.target.value)} />
      </label>
      <p className="text-ui-caption muted">管理 token 只保存在这个标签页中，关闭标签页后要重新输入。</p>
      <button type="submit" className="button-primary" disabled={!token}>进入后台</button>
    </form>
  )
}

export function AdminPage({ onEntered }: { onEntered: (ref: string) => void }) {
  const signedIn = useStore(adminStore, s => s.signedIn)
  const brandName = useStore(brandStore, s => s.name)
  return (
    <div className="page-content admin-page">
      <div className="admin-heading">
        <div className="page-title">
          <span className="text-ui-sm accent">{brandName}</span>
          <h1 className="text-ui-xl">后台</h1>
          <p className="text-ui-base muted">管理品牌名、常驻房间、音源账号和已存储的媒体。</p>
        </div>
        {signedIn && (
          <button type="button" className="button-ghost" onClick={() => adminStore.signOut()}>
            <LogOut size={16} aria-hidden="true" />
            退出后台
          </button>
        )}
      </div>
      {signedIn
        ? (
            <>
              <SiteSettings />
              <PersistentRooms onEntered={onEntered} />
              <SourceAccounts />
              <StoredMedia />
            </>
          )
        : <SignIn />}
    </div>
  )
}
