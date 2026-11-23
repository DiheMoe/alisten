import type { RoomListing } from '@/core/realtime'
import { Headphones, LockKeyhole, Users } from 'lucide-react'
import { useState } from 'react'
import { loadTicket } from '@/core/api'
import { useSubmission } from '@/shared/lib/use-submission'
import { Modal } from '@/shared/ui'
import { roomEntryStore } from './room-entry'
import './rooms.css'

export function JoinDialog({ room, onJoined, onClose }: { room: RoomListing, onJoined?: () => void, onClose: () => void }) {
  const [password, setPassword] = useState('')
  const { busy, submit } = useSubmission()
  const needsPassword = room.hasPassword && !loadTicket(room.id)
  const join = async () => {
    if (await roomEntryStore.join(room, password))
      onJoined?.()
  }
  return (
    <Modal title="加入房间" onClose={onClose}>
      <form
        className="form-stack join-form"
        onSubmit={(event) => {
          event.preventDefault()
          void submit(join)
        }}
      >
        <div className="join-summary">
          <div className="join-art">
            <Headphones size={36} strokeWidth={1.2} aria-hidden="true" />
          </div>
          <div className="join-detail">
            <h3 className="text-ui-lg">{room.name}</h3>
            {room.description && <p className="text-ui-caption muted">{room.description}</p>}
            <span className="join-meta text-ui-sm muted">
              <Users size={14} aria-hidden="true" />
              {`${room.onlineCount} / ${room.capacity} 人在线`}
              {room.onlineCount >= room.capacity && (
                <>
                  <span aria-hidden="true">·</span>
                  已满员
                </>
              )}
              {room.hasPassword && (
                <>
                  <span aria-hidden="true">·</span>
                  <LockKeyhole size={14} aria-hidden="true" />
                  需要密码
                </>
              )}
            </span>
          </div>
        </div>
        {needsPassword && (
          <label>
            房间密码
            <input type="password" autoComplete="current-password" data-autofocus required value={password} onChange={event => setPassword(event.target.value)} placeholder="输入房间密码" />
          </label>
        )}
        <button type="submit" className="button-primary" disabled={busy || (needsPassword && !password)}>
          {busy ? '正在加入…' : '加入房间'}
        </button>
      </form>
    </Modal>
  )
}
