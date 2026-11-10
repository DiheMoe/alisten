import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { brandStore } from '@/core/brand'
import { errorMessage } from '@/core/errors'
import { useStore } from '@/shared/lib/use-store'
import { InlineTextField, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'
import { LoadFailed, SectionHead } from './section-head'

async function saveBrandName(name: string): Promise<void> {
  const saved = await adminStore.attempt(() => api.adminUpdateSite({ brandName: name }), { INVALID_ARGUMENT: '品牌名要有 1 到 20 个字，不能含换行、制表符等特殊字符。' })
  // 显示服务端存下的名字，因为浏览器和服务端去掉首尾空白的规则不完全相同。
  brandStore.setName(saved.brandName)
}

export function SiteSettings() {
  const brandName = useStore(brandStore, s => s.name)
  // undefined 表示正在获取，Error 表示获取失败。
  const [status, setStatus] = useState<'loaded' | Error>()
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let active = true
    brandStore.load().then(() => {
      if (active)
        setStatus('loaded')
    }, (error: unknown) => {
      if (active)
        setStatus(new Error(errorMessage(error)))
    })
    return () => {
      active = false
    }
  }, [attempt])
  const retry = () => {
    setStatus(undefined)
    setAttempt(n => n + 1)
  }
  return (
    <section className="admin-section" aria-labelledby="admin-site-title">
      <SectionHead id="admin-site-title" title="站点" />
      {status === undefined
        ? <Skeleton rows={1} />
        : status instanceof Error
          ? <LoadFailed message={`暂时无法获取品牌名。${status.message}`} onRetry={retry} />
          : (
              <div className="section-group">
                <div className="section-row">
                  <div className="section-row-copy">
                    <span className="section-row-title">品牌名</span>
                    <span className="section-row-description">显示在发现房间、后台页面的顶部和浏览器标签页上，最多 20 个字。已经打开的页面刷新后才会更新。</span>
                  </div>
                  <InlineTextField label="品牌名" value={brandName} onSave={saveBrandName} />
                </div>
              </div>
            )}
    </section>
  )
}
