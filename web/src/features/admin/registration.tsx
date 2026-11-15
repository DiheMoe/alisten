import { useId } from 'react'
import { api } from '@/core/api'
import { useResource } from '@/shared/lib/use-resource'
import { useSettingSave } from '@/shared/lib/use-setting-save'
import { SaveStatus, Skeleton, Switch } from '@/shared/ui'
import { adminStore } from './admin-store'
import { GithubApp } from './github-app'
import { LoadFailed, SectionHead } from './section-head'

// 保存期间不禁用开关，因为禁用会让开关失去焦点，键盘用户就找不到它了。
function OpenRegistrationRow({ open, onSaved }: { open: boolean, onSaved: (open: boolean) => void }) {
  const switchId = useId()
  const descriptionId = useId()
  const errorId = useId()
  const setting = useSettingSave(open, async (next: boolean) => {
    await adminStore.attempt(() => next ? api.adminOpenRegistration() : api.adminCloseRegistration())
    onSaved(next)
  })
  const { error } = setting.state
  return (
    <div className="section-group">
      <div className="section-row">
        <div className="section-row-copy">
          <label htmlFor={switchId} className="section-row-title">开放注册</label>
          <span id={descriptionId} className="section-row-description">关闭后不能注册新账号，已有账号照常登录，游客照常使用。</span>
          {error && <span id={errorId} className="admin-row-error" role="alert">{error}</span>}
        </div>
        <div className="admin-row-actions">
          <SaveStatus status={setting.state.status} version={setting.savedVersion} />
          <Switch id={switchId} checked={setting.value} aria-describedby={error ? `${descriptionId} ${errorId}` : descriptionId} onChange={setting.change} />
        </div>
      </div>
    </div>
  )
}

function OpenRegistration() {
  const open = useResource(() => adminStore.attempt(() => api.getSite()).then(site => site.registrationOpen))
  if (open.state.status === 'failed')
    return <LoadFailed message={`暂时无法获取注册状态。${open.state.error}`} onRetry={open.reload} />
  if (open.state.status === 'loading')
    return <Skeleton rows={1} />
  return <OpenRegistrationRow open={open.state.value} onSaved={open.set} />
}

export function Registration() {
  return (
    <section className="admin-section" aria-labelledby="admin-registration-title">
      <SectionHead id="admin-registration-title" title="登录与注册" />
      <OpenRegistration />
      <h3 className="section-group-label">GitHub 登录</h3>
      <GithubApp />
    </section>
  )
}
