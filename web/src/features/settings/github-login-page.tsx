import { useEffect, useEffectEvent, useState } from 'react'
import { completeGithubLogin } from './account'
import { takeGithubLanding } from './github'
import './settings.css'

// GitHub 授权之后的落地页。onDone 在离开这一页时调用（登录了另一个账号时在重新加载页面之前），由应用壳决定之后停在哪里。
export function GitHubLoginPage({ fragment, onDone }: { fragment: string, onDone: () => void }) {
  const [landing] = useState(() => takeGithubLanding(fragment))
  const [failure, setFailure] = useState(landing.kind === 'failed' ? landing.message : null)
  const done = useEffectEvent(onDone)
  useEffect(() => {
    if (landing.kind !== 'exchange')
      return
    const leaving = new AbortController()
    void completeGithubLogin(landing, leaving.signal, () => done()).then((message) => {
      if (message !== null)
        setFailure(message)
    })
    return () => leaving.abort()
  }, [landing])
  return (
    <div className="page-content settings-pair-page">
      <div className="page-title">
        <h1 className="text-ui-xl">GitHub 登录</h1>
        <p className="text-ui-base muted" role="status">{failure ?? '正在完成 GitHub 登录…'}</p>
      </div>
      {failure && <button type="button" className="button-primary" onClick={onDone}>回到设置</button>}
    </div>
  )
}
