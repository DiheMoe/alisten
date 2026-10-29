import type { RoomListing } from '@/core/realtime'
import { Headphones, LockKeyhole, Users } from 'lucide-react'
import { useState } from 'react'
import { api, loadTicket, saveTicket } from '@/core/api'
import { Modal, notificationStore } from '@/shared/ui'
import { joinErrorMessage } from './room-entry'
import './rooms.css'

export function JoinDialog({ room, onJoined, onClose }: { room: RoomListing, onJoined: () => void, onClose: () => void }) {
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const needsPassword = room.hasPassword && !loadTicket(room.id)
  const submit = async () => {
    setBusy(true)
    try {
      if (!loadTicket(room.id)) {
        const { ticket } = await api.createTicket(room.id, room.hasPassword ? { password } : {})
        saveTicket(room.id, ticket)
      }
      onJoined()
    }
    catch (error) {
      notificationStore.error(joinErrorMessage(error))
      setBusy(false)
    }
  }
  return (
    <Modal title="加入房间" onClose={onClose}>
      <form
        className="form-stack join-form"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
      >
        <div className="join-summary">
          <div className="join-art">
            <Headphones size={36} strokeWidth={1.2} aria-hidden="true" />
          </div>
          <div className="join-detail">
            <h3 className="text-ui-lg">{room.name}</h3>
            <p className="text-ui-caption muted">{room.description || '来这里，听见好音乐。'}</p>
            <span className="join-meta text-ui-sm muted">
              <Users size={14} aria-hidden="true" />
              {room.onlineCount}
              {' '}
              人正在听
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
        <p className="text-ui-caption muted">加入后，与房间内的所有人同步播放、点歌和聊天。</p>
        <button type="submit" className="button-primary" disabled={busy || (needsPassword && !password)}>
          {busy ? '正在加入…' : '加入房间'}
        </button>
      </form>
    </Modal>
  )
}
