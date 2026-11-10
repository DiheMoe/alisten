import type { MediaSort } from '@/core/realtime'
import type { SectionItem } from '@/shared/ui'
import { Database, DoorOpen, KeyRound, LogOut, RadioTower, Store, Users } from 'lucide-react'
import { useState } from 'react'
import { brandStore } from '@/core/brand'
import { useStore } from '@/shared/lib/use-store'
import { SectionLayout } from '@/shared/ui'
import { Accounts } from './accounts'
import { adminStore } from './admin-store'
import { PersistentRooms } from './persistent-rooms'
import { Registration } from './registration'
import { SiteSettings } from './site-settings'
import { needsSignIn } from './source-account-view'
import { SourceAccounts } from './source-accounts'
import { useSourceCredentials } from './source-credentials'
import { MediaPage } from './stored-media'
import './admin.css'

export type AdminSection = 'site' | 'registration' | 'users' | 'rooms' | 'sources' | 'media'

// section 为 null 表示窄屏回到分类列表。没有给出的 q、page、sort 取默认值。
interface AdminView {
  section: AdminSection | null
  q?: string
  page?: number
  sort?: MediaSort
}

interface AdminPageProps {
  section: AdminSection | null
  q: string
  page: number
  sort: MediaSort
  onNavigate: (view: AdminView) => void
  onEntered: (ref: string) => void
}

const sections: SectionItem<AdminSection>[] = [
  { id: 'site', label: '站点', icon: Store },
  { id: 'registration', label: '登录与注册', icon: KeyRound },
  { id: 'users', label: '用户', icon: Users },
  { id: 'rooms', label: '房间', icon: DoorOpen },
  { id: 'sources', label: '音源', icon: RadioTower },
  { id: 'media', label: '媒体', icon: Database },
]

function SignIn() {
  const brandName = useStore(brandStore, s => s.name)
  const [token, setToken] = useState('')
  return (
    <div className="admin-sign-in">
      <form
        className="admin-sign-in-card form-stack"
        aria-labelledby="admin-sign-in-title"
        onSubmit={(event) => {
          event.preventDefault()
          adminStore.signIn(token)
        }}
      >
        <div>
          <p className="admin-eyebrow">{brandName}</p>
          <h1 id="admin-sign-in-title">后台</h1>
        </div>
        <label>
          管理 token
          <input type="password" autoComplete="off" autoFocus required value={token} onChange={event => setToken(event.target.value)} />
        </label>
        <p className="text-ui-caption muted">管理 token 只保存在这个标签页中，关闭标签页后要重新输入。</p>
        <button type="submit" className="button-primary" disabled={!token}>进入后台</button>
      </form>
    </div>
  )
}

function Sections({ section, q, page, sort, onNavigate, onEntered }: AdminPageProps) {
  const brandName = useStore(brandStore, s => s.name)
  const credentials = useSourceCredentials()
  const flagged = Object.values(credentials).some(credential => needsSignIn(credential.state))
  const shown = section ?? 'site'
  return (
    <SectionLayout
      title="后台"
      eyebrow={brandName}
      sections={sections.map(item => item.id === 'sources' ? { ...item, badge: flagged } : item)}
      current={section}
      defaultSection="site"
      onSelect={id => onNavigate({ section: id })}
      footer={(
        <button type="button" className="section-nav-item" onClick={() => adminStore.signOut()}>
          <LogOut size={18} strokeWidth={1.8} aria-hidden="true" />
          退出后台
        </button>
      )}
    >
      {shown === 'site' && <SiteSettings />}
      {shown === 'registration' && <Registration />}
      {shown === 'users' && <Accounts />}
      {shown === 'rooms' && <PersistentRooms q={q} page={page} onFilter={filter => onNavigate({ section: 'rooms', ...filter })} onEntered={onEntered} />}
      {shown === 'sources' && <SourceAccounts credentials={credentials} />}
      {shown === 'media' && <MediaPage q={q} sort={sort} page={page} onNavigate={view => onNavigate({ section: 'media', ...view })} />}
    </SectionLayout>
  )
}

export function AdminPage(props: AdminPageProps) {
  const signedIn = useStore(adminStore, s => s.signedIn)
  return signedIn ? <Sections {...props} /> : <SignIn />
}
