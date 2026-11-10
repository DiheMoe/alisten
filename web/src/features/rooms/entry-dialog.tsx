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
        // onCancel 要在放弃进入之前调用，因为离开时要靠进房的信息认出这个房间的地址。
        onCancel()
        roomEntryStore.reset()
      }}
    />
  )
}
