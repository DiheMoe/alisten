import { useStore } from '@/shared/lib/use-store'
import { JoinDialog } from './join-dialog'
import { roomEntryStore } from './room-entry'

export function EntryDialog({ onCancel }: { onCancel: () => void }) {
  const phase = useStore(roomEntryStore, s => s.phase)
  const room = useStore(roomEntryStore, s => s.room)
  if (phase !== 'needs-ticket' || !room)
    return null
  return (
    <JoinDialog
      room={room}
      onJoined={() => roomEntryStore.retry()}
      onClose={() => {
        // onCancel 要在放弃进入之前：离开这个房间的地址时，要靠进房的信息认出它。
        onCancel()
        roomEntryStore.reset()
      }}
    />
  )
}
