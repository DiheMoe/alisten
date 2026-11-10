import type { RoomListing } from '@/core/realtime'
import { DoorOpen, Ellipsis, KeyRound, LockKeyhole, LogIn, Pencil, Plus, Search, SearchX, Share2, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { api } from '@/core/api'
import { copyInviteLink, roomEntryStore } from '@/features/rooms'
import { rowAfterRemoval } from '@/shared/lib/removal-focus'
import { ActionMenu, ConfirmDialog, EmptyState, Pagination, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'
import { rowOrHeading } from './removal-focus'
import { RoomApiKeyDialog } from './room-api-key-dialog'
import { filterRooms } from './room-filter'
import { RoomFormDialog } from './room-form-dialog'
import { LoadFailed, SectionHead, StatusPill } from './section-head'

function enterRoom(room: RoomListing, onEntered: (ref: string) => void): void {
  const ref = room.slug ?? room.id
  const entry = roomEntryStore.state
  if (entry.phase !== 'joined' || entry.room?.id !== room.id)
    void roomEntryStore.enter(ref, room)
  onEntered(ref)
}

// q 和 page 来自地址。改搜索词时回到第 1 页。
export function PersistentRooms({ q, page, onFilter, onEntered }: {
  q: string
  page: number
  onFilter: (filter: { q: string, page?: number }) => void
  onEntered: (ref: string) => void
}) {
  const [rooms, setRooms] = useState<RoomListing[] | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [reload, setReload] = useState(0)
  const [editing, setEditing] = useState<RoomListing | 'new' | null>(null)
  const [deleting, setDeleting] = useState<RoomListing | null>(null)
  const [keyOf, setKeyOf] = useState<RoomListing | null>(null)
  // 删掉房间之后焦点要落到的那一行，null 表示落到标题。
  const nextFocusRef = useRef<string | null>(null)
  useEffect(() => {
    let active = true
    adminStore.attempt(() => api.adminListRooms()).then((result) => {
      if (active)
        setRooms(result.items)
    }, (error: Error) => {
      if (active)
        setFailure(error.message)
    })
    return () => {
      active = false
    }
  }, [reload])
  const refresh = () => {
    setFailure(null)
    setReload(n => n + 1)
  }
  const retry = () => {
    setRooms(null)
    refresh()
  }
  const shown = rooms && filterRooms(rooms, q, page)
  // 删掉房间或者打开旧链接时，地址里的页码可能超出范围，改成实际显示的那一页。
  const shownPage = shown?.page
  useEffect(() => {
    if (shownPage !== undefined && shownPage !== page)
      onFilter({ q, page: shownPage })
  }, [shownPage, page, q, onFilter])
  return (
    <section className="admin-section" aria-labelledby="admin-rooms-title">
      <SectionHead id="admin-rooms-title" title="常驻房间" description="常驻房间不会因为没人在线而关闭。">
        <label className="admin-search">
          <Search size={16} aria-hidden="true" />
          <input type="search" aria-label="搜索房间" placeholder="搜索房间" value={q} onChange={event => onFilter({ q: event.target.value })} />
        </label>
        <button type="button" className="button-primary" onClick={() => setEditing('new')}>
          <Plus size={16} aria-hidden="true" />
          新建房间
        </button>
      </SectionHead>
      {failure !== null
        ? <LoadFailed message={`暂时无法获取常驻房间。${failure}`} onRetry={retry} />
        : !shown
            ? <Skeleton rows={3} />
            : !shown.items.length
                ? q.trim()
                  ? (
                      <EmptyState icon={SearchX} title="没有找到房间" action={<button type="button" className="button-secondary" onClick={() => onFilter({ q: '' })}>清除搜索</button>}>
                        换个名称或别名再找找。
                      </EmptyState>
                    )
                  : (
                      <EmptyState icon={DoorOpen} title="还没有常驻房间" action={<button type="button" className="button-secondary" onClick={() => setEditing('new')}>新建房间</button>} />
                    )
                : (
                    <>
                      <div className="section-group admin-table">
                        <div className="admin-table-head" aria-hidden="true">
                          <span>房间</span>
                          <span>别名</span>
                          <span>访问</span>
                        </div>
                        <ul>
                          {shown.items.map(room => (
                            <li className="admin-room" key={room.id} data-key={room.id}>
                              <div className="admin-room-name">
                                <strong className="truncate">{room.name}</strong>
                                <span>{room.onlineCount ? `${room.onlineCount} 人在线` : '无人在线'}</span>
                              </div>
                              <span className="admin-room-slug truncate">
                                {room.slug
                                  ? `/r/${room.slug}`
                                  : (
                                      <>
                                        <span aria-hidden="true">—</span>
                                        <span className="sr-only">没有别名</span>
                                      </>
                                    )}
                              </span>
                              <span className="admin-room-access">
                                {room.hasPassword
                                  ? (
                                      <StatusPill tone="neutral">
                                        <LockKeyhole size={12} aria-hidden="true" />
                                        <span className="sr-only">需要</span>
                                        密码
                                      </StatusPill>
                                    )
                                  : <StatusPill tone="neutral">公开</StatusPill>}
                              </span>
                              <ActionMenu
                                actions={[
                                  { icon: LogIn, label: '进入房间', run: () => enterRoom(room, onEntered) },
                                  { icon: Share2, label: '复制邀请链接', run: () => void copyInviteLink(room) },
                                  { icon: Pencil, label: '编辑', run: () => setEditing(room) },
                                  { icon: KeyRound, label: 'API Key', run: () => setKeyOf(room) },
                                  { icon: Trash2, label: '删除', danger: true, run: () => setDeleting(room) },
                                ]}
                                menuLabel={`「${room.name}」的操作`}
                                className="icon-button admin-menu-button"
                                label={`「${room.name}」的更多操作`}
                                title="更多操作"
                              >
                                <Ellipsis size={20} strokeWidth={1.7} aria-hidden="true" />
                              </ActionMenu>
                            </li>
                          ))}
                        </ul>
                      </div>
                      <Pagination page={shown.page} totalPages={shown.totalPages} onPage={next => onFilter({ q, page: next })} />
                    </>
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
          danger
          onConfirm={async () => {
            if (await adminStore.call(() => api.adminDeleteRoom(deleting.id)) === null)
              return
            // 先在本地去掉这个房间，这样对话框关闭时那一行已经不在了，焦点才能落到下一行。之后再重新读取。
            const remaining = rooms?.filter(room => room.id !== deleting.id) ?? []
            const next = filterRooms(remaining, q, page)
            // 删空了这一页时显示的是上一页，那里没有“下一行”。
            nextFocusRef.current = rowAfterRemoval(shown?.items.map(room => room.id) ?? [], next.page === shown?.page ? next.items.map(room => room.id) : [], [deleting.id])
            setRooms(remaining)
            setDeleting(null)
            refresh()
          }}
          onClose={() => setDeleting(null)}
          returnFocus={() => rowOrHeading('admin-rooms-title', nextFocusRef.current)}
        >
          {`删除「${deleting.name}」后，房间里的所有人都会断开连接。`}
        </ConfirmDialog>
      )}
    </section>
  )
}
