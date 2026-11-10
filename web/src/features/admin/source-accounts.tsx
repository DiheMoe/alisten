import type { Credential, Source, SourceId } from './source-credentials'
import { useId, useState } from 'react'
import { notificationStore } from '@/shared/ui'
import { QrLoginDialog } from './qr-login-dialog'
import { SectionHead, StatusPill } from './section-head'
import { afterChinese, sourceAccountView } from './source-account-view'
import { sources } from './source-credentials'
import { YouTubeCookies } from './youtube-cookies'

function SourceCard({ source, credential: { state, reload } }: { source: Source, credential: Credential }) {
  const titleId = useId()
  const [signingIn, setSigningIn] = useState(false)
  const view = sourceAccountView(state, source.name)
  return (
    <article className="admin-card" aria-labelledby={titleId}>
      <div className="admin-card-top">
        <h3 id={titleId}>{source.name}</h3>
        <StatusPill tone={view.tone}>{view.status}</StatusPill>
      </div>
      {view.detail && <p className="admin-card-detail">{view.detail}</p>}
      <div className="admin-card-actions">
        {state instanceof Error
          ? <button type="button" className="button-secondary" onClick={reload}>重试</button>
          : <button type="button" className={state?.set ? 'button-secondary' : 'button-primary'} disabled={state === undefined} onClick={() => setSigningIn(true)}>{state?.set ? '重新登录' : '扫码登录'}</button>}
      </div>
      {signingIn && (
        <QrLoginDialog
          title={`扫码登录${afterChinese(source.name)}`}
          targets={source.targets}
          onSuccess={() => {
            setSigningIn(false)
            notificationStore.success(`已登录${afterChinese(source.name)}`)
            reload()
          }}
          onClose={() => setSigningIn(false)}
        />
      )}
    </article>
  )
}

export function SourceAccounts({ credentials }: { credentials: Record<SourceId, Credential> }) {
  return (
    <section className="admin-section" aria-labelledby="admin-sources-title">
      <SectionHead id="admin-sources-title" title="音源" description="扫码登录各平台的账号，用来搜索和播放它们的歌曲。" />
      <div className="admin-cards">
        {(Object.keys(sources) as SourceId[]).map(id => <SourceCard key={id} source={sources[id]} credential={credentials[id]} />)}
        <YouTubeCookies />
      </div>
    </section>
  )
}
