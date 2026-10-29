import type { RoomListing } from '@/core/realtime'
import { ArrowRight, Headphones, LockKeyhole, Plus, Search, Users } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { brandStore } from '@/core/brand'
import { errorMessage } from '@/core/errors'
import { useStore } from '@/shared/lib/use-store'
import { EmptyState, notificationStore } from '@/shared/ui'
import { CreateRoomDialog } from './create-room-dialog'
import { JoinDialog } from './join-dialog'
import { roomEntryStore } from './room-entry'
import './rooms.css'

function matches(room: RoomListing, query: string): boolean {
  const keyword = query.trim().toLowerCase()
  return !keyword || `${room.name} ${room.description}`.toLowerCase().includes(keyword)
}

export function Lobby({ filter, onFilter, onEntered }: { filter: string, onFilter: (filter: string) => void, onEntered: (ref: string) => void }) {
  const [rooms, setRooms] = useState<RoomListing[] | null>(null)
  const [failed, setFailed] = useState(false)
  const [reload, setReload] = useState(0)
  const [selected, setSelected] = useState<RoomListing | null>(null)
  const [creating, setCreating] = useState(false)
  const brandName = useStore(brandStore, s => s.name)
  useEffect(() => {
    let active = true
    api.listRooms().then(({ items }) => {
      if (active)
        setRooms(items)
    }, (error) => {
      if (!active)
        return
      notificationStore.error(errorMessage(error))
      setFailed(true)
    })
    return () => {
      active = false
    }
  }, [reload])
  const visible = rooms?.filter(room => matches(room, filter)) ?? []
  return (
    <div className="page-content room-page">
      <div className="page-title">
        <span className="text-ui-sm accent">{brandName}</span>
        <h1 className="text-ui-xl">好音乐，值得一起听</h1>
        <p className="text-ui-base muted">找到你的听歌房，与同频的人分享此刻。</p>
      </div>
      <div className="room-toolbar">
        <label className="search-field">
          <Search size={18} aria-hidden="true" />
          <input aria-label="搜索房间" value={filter} onChange={event => onFilter(event.target.value)} placeholder="搜索房间名称或描述" />
        </label>
        <button type="button" className="button-primary" onClick={() => setCreating(true)}>
          <Plus size={18} />
          创建房间
        </button>
      </div>
      <div className="section-heading">
        <h2 className="text-ui-lg">正在发生的音乐</h2>
        <span className="muted text-ui-sm">{rooms ? `${visible.length} 个房间` : '正在寻找房间…'}</span>
      </div>
      {failed
        ? (
            <EmptyState
              title="暂时无法获取房间"
              action={(
                <button
                  type="button"
                  className="button-secondary"
                  onClick={() => {
                    setFailed(false)
                    setRooms(null)
                    setReload(n => n + 1)
                  }}
                >
                  重试
                </button>
              )}
            >
              请稍后重试。
            </EmptyState>
          )
        : !rooms
            ? <RoomSkeleton />
            : !visible.length
                ? (
                    <EmptyState
                      icon={Headphones}
                      title={filter ? '没有找到这个房间' : '成为第一个创建房间的人'}
                      action={<button type="button" className="button-secondary" onClick={() => setCreating(true)}>创建房间</button>}
                    >
                      {filter ? '换个关键词，再找找看。' : '创建一个房间，邀请朋友一起听歌。'}
                    </EmptyState>
                  )
                : (
                    <div className="room-grid">
                      {visible.map(room => (
                        <button type="button" className="room-card" key={room.id} onClick={() => setSelected(room)}>
                          <div className="room-card-art">
                            <div className="room-art">
                              <Headphones size={44} strokeWidth={1.2} aria-hidden="true" />
                            </div>
                            {room.hasPassword && (
                              <span className="room-card-badge text-ui-xs">
                                <LockKeyhole size={12} aria-hidden="true" />
                                密码
                              </span>
                            )}
                            <span className="room-card-join" aria-hidden="true">
                              <ArrowRight size={20} strokeWidth={2} />
                            </span>
                          </div>
                          <div className="room-card-copy">
                            <h3 className="text-ui-base truncate" title={room.name}>{room.name}</h3>
                            <p className="text-ui-caption muted">{room.description || '来这里，听见好音乐。'}</p>
                            <span className="text-ui-sm muted">
                              <Users size={14} aria-hidden="true" />
                              {room.onlineCount}
                              {' '}
                              人正在听
                            </span>
                          </div>
                        </button>
                      ))}
                    </div>
                  )}
      {selected && (
        <JoinDialog
          room={selected}
          onJoined={() => {
            setSelected(null)
            const ref = selected.slug ?? selected.id
            void roomEntryStore.enter(ref, selected)
            onEntered(ref)
          }}
          onClose={() => setSelected(null)}
        />
      )}
      {creating && (
        <CreateRoomDialog
          onCreated={(id) => {
            setCreating(false)
            onEntered(id)
          }}
          onClose={() => setCreating(false)}
        />
      )}
    </div>
  )
}

function RoomSkeleton() {
  return (
    <div className="room-grid" role="status" aria-label="正在加载房间">
      {Array.from({ length: 8 }, (_, i) => (
        <div className="room-card room-skeleton" key={i} aria-hidden="true">
          <div className="room-card-art">
            <div className="room-art" />
          </div>
          <div className="room-card-copy">
            <span className="skeleton-line" />
            <span className="skeleton-line" />
          </div>
        </div>
      ))}
    </div>
  )
}
