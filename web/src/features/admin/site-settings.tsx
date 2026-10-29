import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { brandStore } from '@/core/brand'
import { useStore } from '@/shared/lib/use-store'
import { notificationStore, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'

export function SiteSettings() {
  const brandName = useStore(brandStore, s => s.name)
  const [status, setStatus] = useState<'loading' | 'failed' | 'loaded'>('loading')
  const [attempt, setAttempt] = useState(0)
  const [draft, setDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let active = true
    brandStore.load().then(() => {
      if (active)
        setStatus('loaded')
    }, () => {
      if (active)
        setStatus('failed')
    })
    return () => {
      active = false
    }
  }, [attempt])
  const retry = () => {
    setStatus('loading')
    setAttempt(n => n + 1)
  }
  const name = draft ?? brandName
  const save = async () => {
    setBusy(true)
    try {
      const saved = await adminStore.call(() => api.adminUpdateSite({ brandName: name }), { INVALID_ARGUMENT: '品牌名要有 1 到 20 个字，不能含换行、制表符等特殊字符。' })
      if (saved === null)
        return
      // 显示服务端存下的名字：浏览器和服务端去掉首尾空白的规则不完全相同。
      brandStore.setName(saved.brandName)
      setDraft(null)
      notificationStore.success('已保存品牌名')
    }
    finally {
      setBusy(false)
    }
  }
  return (
    <section className="admin-section" aria-labelledby="admin-site-title">
      <div className="section-heading">
        <h2 id="admin-site-title" className="text-ui-lg">站点</h2>
      </div>
      {status === 'failed'
        ? (
            <div className="admin-empty admin-failed">
              <p className="text-ui-caption muted">暂时无法获取品牌名。</p>
              <button type="button" className="button-secondary" onClick={retry}>重试</button>
            </div>
          )
        : status === 'loading'
          ? <Skeleton rows={1} />
          : (
              <form
                className="admin-list"
                onSubmit={(event) => {
                  event.preventDefault()
                  void save()
                }}
              >
                <div className="admin-row">
                  <div className="admin-row-copy">
                    <label htmlFor="admin-brand-name" className="admin-row-title text-ui-base">品牌名</label>
                    <span id="admin-brand-name-description" className="text-ui-sm muted">显示在发现房间、后台页面的顶部和浏览器标签页上，最多 20 个字。已经打开的页面刷新后才会更新。</span>
                  </div>
                  <div className="admin-row-actions">
                    <input id="admin-brand-name" className="admin-brand-name" aria-describedby="admin-brand-name-description" autoComplete="off" value={name} onChange={event => setDraft(event.target.value)} />
                    <button type="submit" className="button-secondary" disabled={busy || name.trim() === ''}>{busy ? '正在保存…' : '保存'}</button>
                  </div>
                </div>
              </form>
            )}
    </section>
  )
}
