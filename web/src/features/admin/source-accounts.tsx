import type { QrLoginTarget } from './qr-login-dialog'
import type { CredentialState } from './source-account-view'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { notificationStore } from '@/shared/ui'
import { adminStore } from './admin-store'
import { QrLoginDialog } from './qr-login-dialog'
import { afterChinese, sourceAccountView } from './source-account-view'
import { YouTubeCookies } from './youtube-cookies'

interface Source {
  name: string
  credential: () => Promise<NonNullable<CredentialState>>
  targets: QrLoginTarget[]
}

const sources: Source[] = [
  {
    name: 'QQ 音乐',
    credential: () => api.adminGetQqCredential(),
    targets: [
      { label: 'QQ 扫码', start: () => api.adminStartQqLogin({ type: 'qq' }), check: key => api.adminCheckQqLogin({ type: 'qq', key }) },
      { label: '微信扫码', start: () => api.adminStartQqLogin({ type: 'wx' }), check: key => api.adminCheckQqLogin({ type: 'wx', key }) },
    ],
  },
  {
    name: '网易云音乐',
    credential: () => api.adminGetNeteaseCredential(),
    targets: [
      { label: '网易云音乐扫码', start: () => api.adminStartNeteaseLogin(), check: key => api.adminCheckNeteaseLogin({ key }) },
    ],
  },
  {
    name: '哔哩哔哩',
    credential: () => api.adminGetBilibiliCredential(),
    targets: [
      { label: '哔哩哔哩扫码', start: () => api.adminStartBilibiliLogin(), check: key => api.adminCheckBilibiliLogin({ key }) },
    ],
  },
]

function SourceAccount({ source }: { source: Source }) {
  const [credential, setCredential] = useState<CredentialState>()
  const [attempt, setAttempt] = useState(0)
  const [signingIn, setSigningIn] = useState(false)
  useEffect(() => {
    let active = true
    void adminStore.call(source.credential).then((result) => {
      if (active)
        setCredential(result)
    })
    return () => {
      active = false
    }
  }, [source, attempt])
  const view = sourceAccountView(credential, source.name)
  return (
    <li className="admin-row">
      <div className="admin-row-copy">
        <strong className="text-ui-base">{source.name}</strong>
        <span className="text-ui-sm muted" role="status">{view.status}</span>
        {view.details.map(detail => <span key={detail} className="text-ui-sm muted">{detail}</span>)}
      </div>
      <div className="admin-row-actions">
        <button type="button" className="button-secondary" disabled={credential === undefined} onClick={() => setSigningIn(true)}>{credential?.set ? '重新登录' : '扫码登录'}</button>
      </div>
      {signingIn && (
        <QrLoginDialog
          title={`扫码登录${afterChinese(source.name)}`}
          targets={source.targets}
          onSuccess={() => {
            setSigningIn(false)
            notificationStore.success(`已登录${afterChinese(source.name)}`)
            setCredential(undefined)
            setAttempt(n => n + 1)
          }}
          onClose={() => setSigningIn(false)}
        />
      )}
    </li>
  )
}

export function SourceAccounts() {
  return (
    <section className="admin-section" aria-labelledby="admin-accounts-title">
      <div className="section-heading">
        <h2 id="admin-accounts-title" className="text-ui-lg">音源账号</h2>
      </div>
      <ul className="admin-list">
        {sources.map(source => <SourceAccount key={source.name} source={source} />)}
        <YouTubeCookies />
      </ul>
    </section>
  )
}
