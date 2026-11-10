import { useEffect, useEffectEvent, useState } from 'react'
import { IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { notificationStore } from '@/shared/ui'
import { finishGithubLogin, takeGithubLanding } from './github'
import { profileStore, switchIdentity } from './profile-store'
import './settings.css'

// GitHub 授权之后的落地页。onDone 在离开这一页时调用（登录了另一个账号时在重新加载页面之前），由应用壳决定之后停在哪里。
export function GitHubLoginPage({ fragment, onDone }: { fragment: string, onDone: () => void }) {
  const [landing] = useState(() => takeGithubLanding(fragment))
  const [failure, setFailure] = useState(landing.kind === 'failed' ? landing.message : null)
  const done = useEffectEvent(onDone)
  useEffect(() => {
    if (landing.kind !== 'exchange')
      return
    let active = true
    finishGithubLogin(landing.code, landing.nonce, landing.intent).then((result) => {
      if (!active)
        return
      if (result.memberToken) {
        done()
        switchIdentity(result.memberToken)
        return
      }
      // 确认身份不修改账号，回到设置页接着设置用户名和密码。
      if (landing.intent === 'verify') {
        done()
        return
      }
      // 注册或绑定：成员和成员 token 都不变，不必重新加载。能绑定 GitHub 的只有设置了用户名的账号，注册得到的账号没有用户名。
      void profileStore.registered(result.account)
      notificationStore.success(result.account.username ? '已绑定 GitHub' : '已用 GitHub 注册')
      done()
    }, (error: unknown) => {
      if (active)
        setFailure(error instanceof IdentityRenewedError ? 'GitHub 登录没有完成，请回到设置重新发起。' : errorMessage(error))
    })
    return () => {
      active = false
    }
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
