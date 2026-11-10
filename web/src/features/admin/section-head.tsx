import type { ReactNode } from 'react'
import { CloudOff } from 'lucide-react'

// 分类的标题要和内容一起立即渲染，加载中也不例外：从分类栏切换过来之后，焦点要落在它上面。
export function SectionHead({ id, title, description, children }: { id: string, title: string, description?: string, children?: ReactNode }) {
  return (
    <header className="admin-head">
      <div className="admin-head-copy">
        <h2 id={id} tabIndex={-1}>{title}</h2>
        {description && <p className="admin-head-description">{description}</p>}
      </div>
      {children && <div className="admin-head-actions">{children}</div>}
    </header>
  )
}

export function LoadFailed({ message, onRetry }: { message: string, onRetry: () => void }) {
  return (
    <div className="section-group admin-failed" role="alert">
      <CloudOff size={20} strokeWidth={1.7} aria-hidden="true" />
      <p>{message}</p>
      <button type="button" className="button-secondary" onClick={onRetry}>重试</button>
    </div>
  )
}

export type StatusTone = 'success' | 'danger' | 'neutral'

export function StatusPill({ tone, children }: { tone: StatusTone, children: ReactNode }) {
  return <span className="admin-status" data-tone={tone}>{children}</span>
}
