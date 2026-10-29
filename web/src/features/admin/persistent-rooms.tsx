import type { RoomListing } from '@/core/realtime'
import { KeyRound, LockKeyhole, LogIn, Pencil, Plus, Share2, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { copyInviteLink, roomEntryStore } from '@/features/rooms'
import { IconButton, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'
import { ConfirmDialog } from './confirm-dialog'
import { RoomApiKeyDialog } from './room-api-key-dialog'
import { RoomFormDialog } from './room-form-dialog'

export function PersistentRooms({ onEntered }: { onEntered: (ref: string) => void }) {
  const [rooms, setRooms] = useState<RoomListing[] | null>(null)
  const [failed, setFailed] = useState(false)
  const [reload, setReload] = useState(0)
  const [editing, setEditing] = useState<RoomListing | 'new' | null>(null)
  const [deleting, setDeleting] = useState<RoomListing | null>(null)
  const [keyOf, setKeyOf] = useState<RoomListing | null>(null)
  useEffect(() => {
    let active = true
    void adminStore.call(() => api.adminListRooms()).then((result) => {
      if (!active)
        return
      if (result)
        setRooms(result.items)
      else
        setFailed(true)
    })
    return () => {
      active = false
    }
  }, [reload])
  const refresh = () => {
    setFailed(false)
    setReload(n => n + 1)
  }
  const retry = () => {
    setRooms(null)
    refresh()
  }
  return (
    <section className="admin-section" aria-labelledby="admin-rooms-title">
      <div className="section-heading">
        <h2 id="admin-rooms-title" className="text-ui-lg">常驻房间</h2>
        <button type="button" className="button-secondary" onClick={() => setEditing('new')}>
          <Plus size={16} aria-hidden="true" />
          新建常驻房间
        </button>
      </div>
      {failed
        ? (
            <div className="admin-empty admin-failed">
              <p className="text-ui-caption muted">暂时无法获取常驻房间。</p>
              <button type="button" className="button-secondary" onClick={retry}>重试</button>
            </div>
          )
        : !rooms
            ? <Skeleton rows={3} />
            : !rooms.length
                ? <p className="admin-empty text-ui-caption muted">还没有常驻房间。</p>
                : (
                    <ul className="admin-list">
                      {rooms.map(room => (
                        <li className="admin-row" key={room.id}>
                          <div className="admin-row-copy">
                            <strong className="text-ui-base truncate">{room.name}</strong>
                            <span className="admin-row-meta text-ui-sm muted">
                              <span className="truncate">{room.slug ? `/r/${room.slug}` : '没有别名'}</span>
                              {room.hasPassword && (
                                <>
                                  <span aria-hidden="true">·</span>
                                  <LockKeyhole size={12} aria-hidden="true" />
                                  有密码
                                </>
                              )}
                              <span aria-hidden="true">·</span>
                              {`${room.onlineCount} 人在线`}
                            </span>
                          </div>
                          <div className="admin-row-actions">
                            <IconButton
                              icon={LogIn}
                              label={`打开「${room.name}」`}
                              onClick={() => {
                                const ref = room.slug ?? room.id
                                const entry = roomEntryStore.state
                                if (entry.phase !== 'joined' || entry.room?.id !== room.id)
                                  void roomEntryStore.enter(ref, room)
                                onEntered(ref)
                              }}
                            />
                            <IconButton icon={Share2} label={`复制「${room.name}」的邀请链接`} onClick={() => void copyInviteLink(room)} />
                            <IconButton icon={KeyRound} label={`「${room.name}」的 API Key`} onClick={() => setKeyOf(room)} />
                            <IconButton icon={Pencil} label={`编辑「${room.name}」`} onClick={() => setEditing(room)} />
                            <IconButton icon={Trash2} label={`删除「${room.name}」`} onClick={() => setDeleting(room)} />
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
      {editing && (
        <RoomFormDialog
          room={editing === 'new' ? null : editing}
          onSaved={() => {
            setEditing(null)
            refresh()
          }}
          onClose={() => setEditing(null)}
        />
      )}
      {keyOf && <RoomApiKeyDialog room={keyOf} onClose={() => setKeyOf(null)} />}
      {deleting && (
        <ConfirmDialog
          title="删除常驻房间"
          confirmLabel="删除房间"
          onConfirm={async () => {
            if (await adminStore.call(() => api.adminDeleteRoom(deleting.id)) !== null) {
              setDeleting(null)
              refresh()
            }
          }}
          onClose={() => setDeleting(null)}
        >
          {`删除「${deleting.name}」后，房间里的所有人都会断开连接。`}
        </ConfirmDialog>
      )}
    </section>
  )
}
