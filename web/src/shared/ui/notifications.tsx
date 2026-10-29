import { CircleAlert, CircleCheck, Info, X } from 'lucide-react'
import { useStore } from '@/shared/lib/use-store'
import { IconButton } from './icon-button'
import { notificationStore } from './notification-store'

const icons = { success: CircleCheck, error: CircleAlert, info: Info }

export function Notifications() {
  const notifications = useStore(notificationStore, s => s.notifications)
  return (
    <div className="notifications" aria-label="通知">
      {notifications.map(({ id, tone, message }) => {
        const Icon = icons[tone]
        return (
          <div key={id} className={`toast toast-${tone}`} role={tone === 'error' ? 'alert' : 'status'}>
            <Icon size={20} aria-hidden="true" />
            <p className="text-ui-base">{message}</p>
            <IconButton icon={X} label="关闭通知" onClick={() => notificationStore.dismiss(id)} />
          </div>
        )
      })}
    </div>
  )
}
