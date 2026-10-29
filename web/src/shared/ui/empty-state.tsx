import type { LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Music2 } from 'lucide-react'

export function EmptyState({ icon: Icon = Music2, title, children, action }: {
  icon?: LucideIcon
  title: string
  children?: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="empty-state">
      <Icon size={36} strokeWidth={1.2} aria-hidden="true" />
      <h3 className="text-ui-lg">{title}</h3>
      <div className="text-ui-caption muted">{children}</div>
      {action}
    </div>
  )
}
