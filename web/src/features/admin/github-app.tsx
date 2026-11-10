import type { AdminGetGithubCredentialResponse } from '@/core/api'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { ConfirmDialog, notificationStore, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'
import { githubAppDraft } from './github-app-draft'
import { LoadFailed, StatusPill } from './section-head'

async function copyCallbackUrl(url: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(url)
    notificationStore.success('已复制回调地址')
  }
  catch {
    // 非安全上下文没有剪贴板接口，用户也可能拒绝授权。
    notificationStore.info(`请手动复制回调地址：${url}`, 8000)
  }
}

export function GithubApp() {
  // undefined 表示正在获取，Error 表示获取失败。
  const [app, setApp] = useState<AdminGetGithubCredentialResponse | Error>()
  const [attempt, setAttempt] = useState(0)
  // null 表示沿用已保存的 Client ID。
  const [clientId, setClientId] = useState<string | null>(null)
  // 服务端不返回已保存的 Client Secret，而它必须和 Client ID 一起提交，所以每次保存都要重新填写。
  const [clientSecret, setClientSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [closing, setClosing] = useState(false)
  useEffect(() => {
    let active = true
    adminStore.attempt(() => api.adminGetGithubCredential()).then((result) => {
      if (active)
        setApp(result)
    }, (error: Error) => {
      if (active)
        setApp(error)
    })
    return () => {
      active = false
    }
  }, [attempt])
  const refetch = () => {
    setApp(undefined)
    setClientId(null)
    setClientSecret('')
    setAttempt(n => n + 1)
  }
  if (app instanceof Error)
    return <LoadFailed message={`暂时无法获取 GitHub 登录的配置。${app.message}`} onRetry={refetch} />
  if (app === undefined)
    return <Skeleton rows={3} />
  const draft = githubAppDraft(clientId ?? app.clientId ?? '', clientSecret)
  const save = async () => {
    if (draft === null)
      return
    setBusy(true)
    try {
      const saved = await adminStore.call(() => api.adminSetGithubCredential(draft), { INVALID_ARGUMENT: 'Client ID 和 Client Secret 只能包含字母、数字、点、下划线和连字符，请从 GitHub 重新复制。' })
      if (saved === null)
        return
      refetch()
      notificationStore.success(app.clientId === null ? '已开启 GitHub 登录' : '已保存 GitHub OAuth App')
    }
    finally {
      setBusy(false)
    }
  }
  return (
    <>
      <form
        className="section-group"
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <div className="section-row">
          <div className="section-row-copy">
            <span className="section-row-title">状态</span>
            <span className="section-row-description">
              {app.clientId === null
                ? '在 GitHub 的 Settings → Developer settings → OAuth Apps 新建一个 OAuth App，填好下面的 Client ID 和 Client Secret 就会开启。'
                : '用户可以用 GitHub 登录、注册和绑定账号。'}
            </span>
          </div>
          <StatusPill tone={app.clientId === null ? 'neutral' : 'success'}>{app.clientId === null ? '未开启' : '已开启'}</StatusPill>
        </div>
        <div className="section-row">
          <div className="section-row-copy">
            <span className="section-row-title">回调地址</span>
            <span className="section-row-description">OAuth App 的 Authorization callback URL 填这个地址。</span>
          </div>
          <div className="admin-row-actions">
            <code className="admin-code">{app.callbackUrl}</code>
            <button type="button" className="button-secondary" onClick={() => void copyCallbackUrl(app.callbackUrl)}>复制</button>
          </div>
        </div>
        <div className="section-row">
          <div className="section-row-copy">
            <label htmlFor="admin-github-client-id" className="section-row-title">Client ID</label>
          </div>
          <input id="admin-github-client-id" className="admin-input" autoComplete="off" spellCheck={false} value={clientId ?? app.clientId ?? ''} onChange={event => setClientId(event.target.value)} />
        </div>
        <div className="section-row">
          <div className="section-row-copy">
            <label htmlFor="admin-github-client-secret" className="section-row-title">Client Secret</label>
            <span id="admin-github-client-secret-description" className="section-row-description">
              {app.clientSecretSet ? '已设置。已保存的不会显示，保存时要和 Client ID 一起重新填写。' : '保存后不会再显示。'}
            </span>
          </div>
          <input id="admin-github-client-secret" className="admin-input" type="password" autoComplete="off" spellCheck={false} aria-describedby="admin-github-client-secret-description" placeholder={app.clientSecretSet ? '已设置' : undefined} value={clientSecret} onChange={event => setClientSecret(event.target.value)} />
        </div>
        <div className="section-row admin-form-actions">
          {app.clientId !== null && <button type="button" className="button-secondary admin-danger" onClick={() => setClosing(true)}>关闭 GitHub 登录</button>}
          <button type="submit" className="button-primary" disabled={busy || draft === null}>{busy ? '正在保存…' : app.clientId === null ? '开启' : '保存'}</button>
        </div>
      </form>
      {/* 关闭之后重新读取配置，整个表单换成骨架屏，所以焦点落到分类标题。 */}
      {closing && (
        <ConfirmDialog
          title="关闭 GitHub 登录"
          confirmLabel="关闭"
          danger
          onConfirm={async () => {
            if (await adminStore.call(() => api.adminDeleteGithubCredential()) === null)
              return
            setClosing(false)
            refetch()
            notificationStore.success('已关闭 GitHub 登录')
          }}
          onClose={() => setClosing(false)}
          returnFocus={() => document.getElementById('admin-registration-title')}
        >
          关闭后，不能再用 GitHub 登录、注册或绑定，已经绑定的 GitHub 保持绑定。只绑定了 GitHub 的账号，关闭之后就无法用 GitHub 登录，重新开启之前也无法在其他设备上登录。
        </ConfirmDialog>
      )}
    </>
  )
}
