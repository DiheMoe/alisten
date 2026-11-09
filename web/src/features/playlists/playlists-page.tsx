import type { LucideIcon } from 'lucide-react'
import type { PlaylistSort, SortKey, SortOrder } from './playlist-sort'
import type { ViewPreference } from './view-preference'
import type { MyPlaylist } from '@/core/realtime'
import { ArrowDown, ArrowDownAZ, ArrowDownNarrowWide, ArrowDownWideNarrow, ArrowDownZA, ArrowUp, LayoutGrid, List, Plus, Star } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { errorMessage } from '@/core/errors'
import { profileStore } from '@/features/settings'
import { useStore } from '@/shared/lib/use-store'
import { EmptyState, IconButton, Modal, notificationStore, Select, Skeleton } from '@/shared/ui'
import { CreatePlaylistForm } from './create-playlist-form'
import { FavoritesMark, PlaylistCover } from './playlist-cover'
import { formatPlaylistDate } from './playlist-date'
import { PlaylistMenu } from './playlist-menu'
import { defaultOrder, sortKeys, sortPlaylists } from './playlist-sort'
import { playlistsStore } from './playlists-store'
import { loadViewPreference, saveViewPreference } from './view-preference'
import './playlists.css'

const sortLabels: Record<SortKey, string> = { name: '名称', createdAt: '创建时间', updatedAt: '更新时间', trackCount: '歌曲数' }
const sortOptions = sortKeys.map(value => ({ value, label: sortLabels[value] }))
const columns: SortKey[] = ['name', 'trackCount', 'updatedAt', 'createdAt']

const orderLabels: Record<SortKey, Record<SortOrder, string>> = {
  name: { asc: 'A–Z', desc: 'Z–A' },
  createdAt: { asc: '最早在前', desc: '最新在前' },
  updatedAt: { asc: '最早在前', desc: '最新在前' },
  trackCount: { asc: '最少在前', desc: '最多在前' },
}

function orderIcon({ key, order }: PlaylistSort): LucideIcon {
  if (key === 'name')
    return order === 'asc' ? ArrowDownAZ : ArrowDownZA
  return order === 'asc' ? ArrowDownNarrowWide : ArrowDownWideNarrow
}

function reversed(order: SortOrder): SortOrder {
  return order === 'asc' ? 'desc' : 'asc'
}

function loadProfile(): void {
  void profileStore.load().catch((error: unknown) => {
    notificationStore.error(errorMessage(error))
  })
}

export function PlaylistsPage({ onOpen, onRegister }: { onOpen: (playlist: MyPlaylist) => void, onRegister: () => void }) {
  const member = useStore(profileStore, s => s.member)
  const account = useStore(profileStore, s => s.account)
  const playlists = useStore(playlistsStore, s => s.playlists)
  const error = useStore(playlistsStore, s => s.error)
  const profileFailed = useStore(profileStore, s => s.failed)
  const [preference, setPreference] = useState(loadViewPreference)
  const [creating, setCreating] = useState(false)
  // 删除歌单之后要获得焦点的歌单（null 表示标题）；每次删除换一个新对象，让同一个目标也能再次触发。
  const [refocus, setRefocus] = useState<{ id: string | null } | null>(null)
  const titleRef = useRef<HTMLHeadingElement>(null)
  const now = useStore(playlistsStore, s => s.loadedAt)
  useEffect(() => {
    if (!member)
      loadProfile()
  }, [member])
  useEffect(() => {
    if (account)
      void playlistsStore.load()
  }, [account])
  useEffect(() => {
    if (!refocus)
      return
    const row = refocus.id === null ? null : document.querySelector<HTMLElement>(`[data-playlist-open="${CSS.escape(refocus.id)}"]`)
    const target = row ?? titleRef.current
    target?.focus()
  }, [refocus])
  const { view, sort } = preference
  const onDeleted = (deleted: MyPlaylist) => {
    const rows = sortPlaylists(playlists ?? [], sort)
    const at = rows.findIndex(p => p.id === deleted.id)
    setRefocus({ id: (rows[at + 1] ?? rows[at - 1])?.id ?? null })
  }
  const update = (next: ViewPreference) => {
    saveViewPreference(next)
    setPreference(next)
  }
  const sortBy = (key: SortKey) => update({ view, sort: { key, order: key === sort.key ? reversed(sort.order) : defaultOrder(key) } })
  const shown = account ? playlists : null
  let body
  if (!member) {
    body = profileFailed
      ? <button type="button" className="button-secondary" onClick={loadProfile}>重试</button>
      : <Skeleton rows={4} />
  }
  else if (!account) {
    body = (
      <EmptyState icon={Star} title="收藏你喜欢的歌" action={<button type="button" className="button-secondary" onClick={onRegister}>去注册</button>}>
        注册账号之后，就能收藏歌曲、建自己的歌单。
      </EmptyState>
    )
  }
  else if (playlists === null) {
    body = error === null
      ? <Skeleton rows={4} />
      : (
          <div className="playlists-error">
            <p className="text-ui-base muted">{`读取失败：${error}`}</p>
            <button type="button" className="button-secondary" onClick={() => void playlistsStore.load()}>重试</button>
          </div>
        )
  }
  else {
    const sorted = sortPlaylists(playlists, sort)
    body = view === 'list'
      ? <PlaylistTable playlists={sorted} sort={sort} now={now} onSort={sortBy} onOpen={onOpen} onDeleted={onDeleted} />
      : <PlaylistGrid playlists={sorted} now={now} onOpen={onOpen} onDeleted={onDeleted} />
  }
  return (
    <section className="page-content playlists-page" aria-labelledby="playlists-title">
      <header className="playlists-toolbar">
        <div className="playlists-heading">
          <h1 ref={titleRef} id="playlists-title" tabIndex={-1} className="text-ui-xl">我的歌单</h1>
          {shown && <p className="text-ui-sm muted">{`${shown.length} 个歌单`}</p>}
        </div>
        {shown && (
          <>
            <div className="playlists-controls">
              <div className="playlists-view-switch" role="group" aria-label="视图">
                <IconButton icon={List} label="列表" aria-pressed={view === 'list'} onClick={() => update({ view: 'list', sort })} />
                <IconButton icon={LayoutGrid} label="网格" aria-pressed={view === 'grid'} onClick={() => update({ view: 'grid', sort })} />
              </div>
              <div className="playlists-sort">
                <Select
                  className="playlists-sort-key"
                  ariaLabel={`排序方式：${sortLabels[sort.key]}`}
                  value={sort.key}
                  options={sortOptions}
                  onChange={(value) => {
                    const key = value as SortKey
                    update({ view, sort: { key, order: key === sort.key ? sort.order : defaultOrder(key) } })
                  }}
                />
                <IconButton
                  className="playlists-sort-order"
                  icon={orderIcon(sort)}
                  label={orderLabels[sort.key][sort.order]}
                  onClick={() => update({ view, sort: { key: sort.key, order: reversed(sort.order) } })}
                />
              </div>
            </div>
            <button type="button" className="button-primary playlists-create" onClick={() => setCreating(true)}>
              <Plus size={18} strokeWidth={2} aria-hidden="true" />
              新建歌单
            </button>
          </>
        )}
      </header>
      {body}
      {creating && <CreateDialog onClose={() => setCreating(false)} />}
    </section>
  )
}

function PlaylistTable({ playlists, sort, now, onSort, onOpen, onDeleted }: {
  playlists: MyPlaylist[]
  sort: PlaylistSort
  now: number
  onSort: (key: SortKey) => void
  onOpen: (playlist: MyPlaylist) => void
  onDeleted: (playlist: MyPlaylist) => void
}) {
  return (
    <div className="playlist-table" role="table" aria-label="我的歌单">
      <div className="playlist-table-head" role="rowgroup">
        <div className="playlist-table-row" role="row">
          {columns.map((key) => {
            const active = key === sort.key
            const Arrow = sort.order === 'asc' ? ArrowUp : ArrowDown
            return (
              <div key={key} className={`playlist-col-${key}`} role="columnheader" aria-sort={active ? (sort.order === 'asc' ? 'ascending' : 'descending') : undefined}>
                <button type="button" className="playlist-sort-button text-ui-sm" data-active={active || undefined} onClick={() => onSort(key)}>
                  {sortLabels[key]}
                  {active && <Arrow size={14} strokeWidth={2} aria-hidden="true" />}
                </button>
              </div>
            )
          })}
          <div className="playlist-col-menu" role="columnheader"><span className="sr-only">操作</span></div>
        </div>
      </div>
      <div role="rowgroup">
        {playlists.map(playlist => (
          <div key={playlist.id} className="playlist-table-row playlist-row" role="row">
            <div className="playlist-col-name" role="cell">
              <button type="button" className="playlist-row-open" data-playlist-open={playlist.id} onClick={() => onOpen(playlist)}>
                <PlaylistCover cover={playlist.coverUrl} favorites={playlist.favorites} size="sm" />
                <span className="playlist-row-copy">
                  <span className="playlist-row-title">
                    <strong className="text-ui-base truncate">{playlist.name}</strong>
                    {playlist.favorites && <FavoritesMark />}
                  </span>
                  <span className="playlist-row-count text-ui-sm muted">{`${playlist.trackCount} 首`}</span>
                </span>
              </button>
            </div>
            <div className="playlist-col-trackCount text-ui-sm muted" role="cell">{playlist.trackCount}</div>
            <div className="playlist-col-updatedAt text-ui-sm muted" role="cell">{formatPlaylistDate(playlist.updatedAtMs, now)}</div>
            <div className="playlist-col-createdAt text-ui-sm muted" role="cell">{formatPlaylistDate(playlist.createdAtMs, now)}</div>
            <div className="playlist-col-menu" role="cell">
              {!playlist.favorites && <PlaylistMenu playlist={playlist} onDeleted={() => onDeleted(playlist)} />}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

function PlaylistGrid({ playlists, now, onOpen, onDeleted }: { playlists: MyPlaylist[], now: number, onOpen: (playlist: MyPlaylist) => void, onDeleted: (playlist: MyPlaylist) => void }) {
  return (
    <ul className="playlist-grid">
      {playlists.map(playlist => (
        <li key={playlist.id} className="playlist-card">
          <button type="button" className="playlist-card-open" data-playlist-open={playlist.id} onClick={() => onOpen(playlist)}>
            <span className="playlist-card-art">
              <PlaylistCover cover={playlist.coverUrl} favorites={playlist.favorites} size="md" />
            </span>
            <strong className="text-ui-base truncate">{playlist.name}</strong>
            <span className="text-ui-sm muted truncate">{`${playlist.trackCount} 首 · ${formatPlaylistDate(playlist.updatedAtMs, now)}`}</span>
          </button>
          {!playlist.favorites && <PlaylistMenu playlist={playlist} className="playlist-card-menu" onDeleted={() => onDeleted(playlist)} />}
        </li>
      ))}
    </ul>
  )
}

function CreateDialog({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="新建歌单" onClose={onClose}>
      <CreatePlaylistForm onCreated={onClose} onCancel={onClose} />
    </Modal>
  )
}
