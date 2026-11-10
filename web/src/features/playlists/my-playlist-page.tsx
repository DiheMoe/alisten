import type { MovePlan } from './track-order'
import type { MyPlaylist, MyPlaylistItem, Track } from '@/core/realtime'
import type { SongAction } from '@/shared/ui'
import { ArrowLeft, CirclePlus, ListMusic, ListPlus, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { roomStore } from '@/core/room'
import { enqueue } from '@/features/queue'
import { bookTitle } from '@/shared/lib/book-title'
import { useStore } from '@/shared/lib/use-store'
import { ConfirmDialog, EmptyState, notificationStore, Skeleton } from '@/shared/ui'
import { FavoritesMark, PlaylistCover } from './playlist-cover'
import { formatPlaylistDate } from './playlist-date'
import { exportFileName, exportPlaylist } from './playlist-file'
import { PlaylistMenu } from './playlist-menu'
import { PlaylistTracks } from './playlist-tracks'
import { playlistsStore, trackKey } from './playlists-store'
import { saveToPlaylistLabel } from './save-label'
import { SaveToPlaylistDialog } from './save-to-playlist'
import { SelectionBar } from './selection-bar'
import { TrackEdits } from './track-edits'
import { dropAt } from './track-order'
import { noSelection, retain, selectAll } from './track-selection'
import './playlists.css'

function keysOf(items: readonly MyPlaylistItem[]): string[] {
  return items.map(item => trackKey(item.track))
}

// 导出服务端的曲目：页面上的曲目可能还没读到，或者带着没同步完的移动和移出。
async function exportToFile(playlist: MyPlaylist): Promise<void> {
  try {
    const { items } = await api.listMyPlaylistItems(playlist.id)
    const url = URL.createObjectURL(new Blob([exportPlaylist(playlist.name, items, Date.now())], { type: 'application/json' }))
    const link = document.createElement('a')
    link.href = url
    link.download = exportFileName(playlist.name)
    link.click()
    // 下载在 click() 时已经开始，到下一个任务再释放对象 URL。
    setTimeout(() => URL.revokeObjectURL(url))
  }
  catch (error) {
    notificationStore.error(`没能导出：${errorMessage(error)}`)
  }
}

export function MyPlaylistPage({ id, onBack }: { id: string, onBack: () => void }) {
  const playlist = useStore(playlistsStore, s => s.playlists?.find(p => p.id === id) ?? null)
  const listed = useStore(playlistsStore, s => s.playlists !== null)
  const connected = useStore(roomStore, s => s.status === 'open')
  const ephemeral = useStore(roomStore, s => s.room?.room.kind === 'ephemeral')
  const playing = useStore(roomStore, s => s.room?.nowPlaying?.entry.track)
  const [items, setItems] = useState<MyPlaylistItem[] | null>(null)
  const [failed, setFailed] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [selection, setSelection] = useState(noSelection)
  // 点了“选择”：没有选中项时也显示勾选框。有选中项时总是处于多选模式。
  const [picking, setPicking] = useState(false)
  const [saving, setSaving] = useState<{ track: Track, title: string } | null>(null)
  const [savingSelected, setSavingSelected] = useState(false)
  const [confirmingRemoval, setConfirmingRemoval] = useState(false)
  const [edits] = useState(() => new TrackEdits(() => setAttempt(n => n + 1)))
  // 歌单的更新时间变了就重新读取：加入、移出、移动和复制都会推进它，歌曲数不变时顺序也可能变了。
  useEffect(() => {
    let active = true
    const usable = edits.read()
    api.listMyPlaylistItems(id).then(({ items }) => {
      if (!active || !usable())
        return
      setItems(items)
      setFailed(null)
      setSelection(selection => retain(selection, keysOf(items)))
    }, (error: unknown) => {
      if (active && usable())
        setFailed(errorMessage(error))
    })
    return () => {
      active = false
    }
  }, [id, attempt, playlist?.updatedAtMs, edits])
  useEffect(() => {
    const refresh = () => setAttempt(n => n + 1)
    window.addEventListener('focus', refresh)
    return () => window.removeEventListener('focus', refresh)
  }, [])
  const selecting = picking || selection.keys.size > 0
  // 对话框和菜单自己处理 Esc。
  useEffect(() => {
    if (!selecting)
      return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || (event.target instanceof Element && event.target.closest('dialog, [popover]')))
        return
      setSelection(noSelection)
      setPicking(false)
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [selecting])
  const now = useStore(playlistsStore, s => s.loadedAt)
  // 读到了自己的歌单，里面却没有它：在别处删掉了，或者换了账号。
  const gone = listed && !playlist
  // 读不到时不再显示之前的曲目：它们可能已经不在了，也可能属于之前的账号。
  const shown = gone || failed ? null : items

  const exitSelection = () => {
    setSelection(noSelection)
    setPicking(false)
  }

  const move = (target: MyPlaylist, current: MyPlaylistItem[], plan: MovePlan<string>) => {
    const byKey = new Map(current.map(item => [trackKey(item.track), item]))
    const trackOf = (key: string) => byKey.get(key)!.track
    const tracks = plan.moved.map(trackOf)
    const before = plan.before === undefined ? undefined : trackOf(plan.before)
    setItems(plan.order.map(key => byKey.get(key)!))
    void edits.run(() => playlistsStore.moveItems(target, tracks, before), () => setItems(current))
  }

  const remove = async (target: MyPlaylist, current: MyPlaylistItem[], tracks: Track[]) => {
    const gone = new Set(tracks.map(trackKey))
    const rest = current.filter(item => !gone.has(trackKey(item.track)))
    const previousSelection = selection
    setItems(rest)
    setSelection(retain(selection, keysOf(rest)))
    await edits.run(() => playlistsStore.removeItems(target, tracks), () => {
      setItems(current)
      setSelection(previousSelection)
    })
  }

  let body
  if (gone) {
    body = (
      <EmptyState
        icon={ListMusic}
        title="这个歌单不存在了"
        action={<button type="button" className="button-secondary" onClick={onBack}>返回我的歌单</button>}
      />
    )
  }
  else if (failed) {
    body = (
      <EmptyState
        icon={ListMusic}
        title={failed}
        action={(
          <button
            type="button"
            className="button-secondary"
            onClick={() => {
              setFailed(null)
              setAttempt(n => n + 1)
            }}
          >
            重试
          </button>
        )}
      />
    )
  }
  else if (items === null) {
    body = <Skeleton rows={6} />
  }
  else if (items.length === 0) {
    body = (
      <EmptyState icon={ListMusic} title="这个歌单还是空的">
        在播放栏、播放列表和播放历史里，可以把歌保存到歌单。
      </EmptyState>
    )
  }
  else {
    const actions = ({ track, meta }: MyPlaylistItem): SongAction[] => {
      const blocked = track.source === 'link' && ephemeral
      return [
        {
          icon: CirclePlus,
          label: blocked ? '临时房间不能点链接里的歌' : `点歌${bookTitle(meta.title)}`,
          disabled: !connected || blocked,
          run: async () => {
            await enqueue(track)
          },
        },
        {
          icon: ListPlus,
          label: saveToPlaylistLabel(meta.title),
          secondary: true,
          run: async () => setSaving({ track, title: meta.title }),
        },
        {
          icon: Trash2,
          label: `把${bookTitle(meta.title)}移出这个歌单`,
          disabled: !playlist,
          secondary: true,
          run: async () => {
            if (playlist)
              await remove(playlist, items, [track])
          },
        },
      ]
    }
    body = (
      <PlaylistTracks
        items={items}
        playing={playing}
        selection={selection}
        selecting={selecting}
        actions={actions}
        onSelect={setSelection}
        onMove={(plan) => {
          if (playlist)
            move(playlist, items, plan)
        }}
      />
    )
  }

  const keys = items ? keysOf(items) : []
  const selected = items?.filter(item => selection.keys.has(trackKey(item.track))).map(item => item.track) ?? []
  return (
    <section className="page-content my-playlist-page" aria-labelledby="my-playlist-title">
      <button type="button" className="button-ghost my-playlist-back text-ui-sm" aria-label="返回我的歌单" onClick={onBack}>
        <ArrowLeft size={16} strokeWidth={1.7} aria-hidden="true" />
        我的歌单
      </button>
      <header className="my-playlist-header">
        <PlaylistCover cover={playlist?.coverUrl ?? null} favorites={playlist?.favorites ?? false} size="lg" />
        <div className="my-playlist-info">
          <h1 id="my-playlist-title" className="my-playlist-name">
            {playlist || listed ? <span>{playlist?.name ?? '歌单'}</span> : <span className="my-playlist-name-pending" role="img" aria-label="正在加载" />}
            {playlist?.favorites && <FavoritesMark size={22} />}
          </h1>
          {playlist && (
            <p className="text-ui-base muted">
              {`${playlist.trackCount} 首，更新于${formatPlaylistDate(playlist.updatedAtMs, now)}，创建于${formatPlaylistDate(playlist.createdAtMs, now)}`}
            </p>
          )}
          <div className="my-playlist-actions">
            {shown && shown.length > 0 && (
              <button type="button" className="button-secondary my-playlist-select" onClick={() => selecting ? exitSelection() : setPicking(true)}>
                {selecting ? '完成' : '选择'}
              </button>
            )}
            {playlist && <PlaylistMenu playlist={playlist} align="start" className="my-playlist-menu" onDeleted={onBack} onExport={() => void exportToFile(playlist)} />}
          </div>
        </div>
      </header>
      {body}
      {/* 选中数的播报常驻在页面上：选择栏要等选中第一首才出现，跟着它出现的播报区域读屏软件不会播报。 */}
      <p className="sr-only" role="status">{shown && selected.length > 0 ? `已选 ${selected.length} 首` : ''}</p>
      {!connected && shown?.length ? <p className="text-ui-caption muted my-playlist-hint">进入房间之后才能点歌。</p> : null}
      {playlist && shown && selected.length > 0 && (
        <SelectionBar
          count={selected.length}
          allSelected={selected.length === shown.length}
          onSave={() => setSavingSelected(true)}
          onTop={() => {
            const plan = dropAt(keys, selection.keys, 0)
            if (plan)
              move(playlist, shown, plan)
          }}
          onBottom={() => {
            const plan = dropAt(keys, selection.keys, keys.length)
            if (plan)
              move(playlist, shown, plan)
          }}
          onRemove={() => {
            if (selected.length > 1)
              setConfirmingRemoval(true)
            else
              void remove(playlist, shown, selected)
          }}
          onToggleAll={() => setSelection(selected.length === shown.length ? noSelection : selectAll(keys))}
          onClose={exitSelection}
        />
      )}
      {saving && <SaveToPlaylistDialog track={saving.track} title={saving.title} onClose={() => setSaving(null)} />}
      {playlist && savingSelected && <SaveToPlaylistDialog from={playlist} tracks={selected} onClose={() => setSavingSelected(false)} />}
      {playlist && shown && confirmingRemoval && (
        <ConfirmDialog
          title="移出歌曲"
          confirmLabel="移出"
          danger
          onConfirm={async () => {
            setConfirmingRemoval(false)
            void remove(playlist, shown, selected)
          }}
          onClose={() => setConfirmingRemoval(false)}
        >
          {`把 ${selected.length} 首歌移出这个歌单？`}
        </ConfirmDialog>
      )}
    </section>
  )
}
