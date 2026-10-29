import type { ReactNode } from 'react'
import { useStore } from '@/shared/lib/use-store'
import { EmptyState, Skeleton } from '@/shared/ui'
import { roomEntryStore } from './room-entry'

export function RoomGate({ children, onBrowse }: { children: ReactNode, onBrowse: () => void }) {
  const phase = useStore(roomEntryStore, s => s.phase)
  const browse = <button type="button" className="button-primary" onClick={onBrowse}>发现房间</button>
  switch (phase) {
    case 'joined':
      return children
    case 'not-found':
      return <EmptyState title="房间不存在或已关闭" action={browse}>换一个房间，继续听歌。</EmptyState>
    case 'failed':
      return (
        <EmptyState title="暂时无法加入房间" action={<button type="button" className="button-primary" onClick={() => roomEntryStore.retry()}>重试</button>}>
          请稍后重试。
        </EmptyState>
      )
    default:
      return <div className="page-content"><Skeleton rows={6} /></div>
  }
}
