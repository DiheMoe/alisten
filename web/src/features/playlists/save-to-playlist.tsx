import type { PlaylistSort } from './playlist-sort'
import type { MyPlaylist, Track } from '@/core/realtime'
import { ListPlus, Plus } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { profileStore } from '@/features/settings'
import { useStore } from '@/shared/lib/use-store'
import { IconButton, Modal, notificationStore, Skeleton } from '@/shared/ui'
import { AccountPrompt } from './account-prompt'
import { CreatePlaylistForm } from './create-playlist-form'
import { FavoritesMark, PlaylistCover } from './playlist-cover'
import { sortPlaylists } from './playlist-sort'
import { playlistsStore, trackKey } from './playlists-store'
import { saveToPlaylistLabel } from './save-label'
import './playlists.css'

const byUpdate: PlaylistSort = { key: 'updatedAt', order: 'desc' }
const none: ReadonlySet<string> = new Set()

// 保持打开时的先后：保存会更新歌单的更新时间，按实时的时间排，刚勾选的歌单会跳走。打开之后才出现的歌单排在最后。
function arrange(playlists: readonly MyPlaylist[], order: readonly string[]): MyPlaylist[] {
  const rank = new Map(order.map((id, index) => [id, index]))
  const at = (playlist: MyPlaylist) => rank.get(playlist.id) ?? order.length
  return sortPlaylists(playlists, byUpdate).sort((a, b) => at(a) - at(b))
}

function savedMessage(saved: readonly MyPlaylist[], skipped: number): string {
  const where = saved.length === 1 ? `“${saved[0].name}”` : ` ${saved.length} 个歌单`
  return `已保存到${where}${skipped > 0 ? `，其中 ${skipped} 首已经在里面` : ''}`
}

// 一首歌：勾选立即生效。歌单里选中的多首：勾好目标歌单后一起保存，来源歌单不在列表里。
type SaveToPlaylistProps = { onClose: () => void } & ({ track: Track, title: string } | { from: MyPlaylist, tracks: Track[] })

export function SaveToPlaylistDialog(props: SaveToPlaylistProps) {
  const { onClose } = props
  const track = 'track' in props ? props.track : null
  const from = 'from' in props ? props.from : null
  const account = useStore(profileStore, s => s.account)
  const playlists = useStore(playlistsStore, s => s.playlists)
  const error = useStore(playlistsStore, s => s.error)
  const included = useStore(playlistsStore, s => track ? s.memberships.get(trackKey(track)) : undefined) ?? none
  const [picked, setPicked] = useState<ReadonlySet<string>>(none)
  const [order, setOrder] = useState<string[] | null>(null)
  const [readFailed, setReadFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [creating, setCreating] = useState(false)
  const [saving, setSaving] = useState(false)
  const listRef = useRef<HTMLUListElement>(null)
  // 从新建页回到列表时要获得焦点的元素：取消时是“新建歌单”，建好之后是新歌单的勾选框，聚焦的同时把它滚进视野。
  const returnFocusRef = useRef<string | null>(null)
  // 打开时刷新歌单：歌曲数和其他设备上新建的歌单。
  useEffect(() => {
    if (!account)
      return
    let active = true
    void Promise.all([playlistsStore.load(), track ? playlistsStore.containing(track) : true]).then(([, read]) => {
      if (!active)
        return
      setReadFailed(!read)
      setOrder(sortPlaylists(playlistsStore.state.playlists ?? [], byUpdate).map(p => p.id))
    })
    return () => {
      active = false
    }
  }, [account, track, attempt])
  useEffect(() => {
    if (creating || returnFocusRef.current === null)
      return
    listRef.current?.querySelector<HTMLElement>(returnFocusRef.current)?.focus()
    returnFocusRef.current = null
  }, [creating])

  const rows = playlists && order ? arrange(playlists, order).filter(p => p.id !== from?.id) : null

  const choose = (playlist: MyPlaylist, checked: boolean) => {
    if (track) {
      void playlistsStore.setMembership(playlist, track, checked)
      return
    }
    setPicked((current) => {
      const next = new Set(current)
      if (checked)
        next.add(playlist.id)
      else
        next.delete(playlist.id)
      return next
    })
  }

  const backToList = (focus: string) => {
    returnFocusRef.current = focus
    setCreating(false)
  }

  const save = async (source: MyPlaylist, tracks: Track[], targets: MyPlaylist[]) => {
    setSaving(true)
    const results = await Promise.all(targets.map(target => playlistsStore.copyItems(source, target, tracks)))
    const saved = targets.filter((_, index) => results[index] !== null)
    const skipped = results.reduce<number>((sum, added) => added === null ? sum : sum + tracks.length - added, 0)
    if (saved.length > 0)
      notificationStore.success(savedMessage(saved, skipped))
    onClose()
  }

  let body
  if (!account) {
    body = <AccountPrompt message="注册或登录之后就能把歌保存到歌单。" onClose={onClose} />
  }
  else if (readFailed || (playlists === null && error !== null)) {
    body = (
      <>
        <p className="text-ui-base muted">{error === null ? '读取失败' : `读取失败：${error}`}</p>
        <button
          type="button"
          className="button-secondary"
          onClick={() => {
            setReadFailed(false)
            setOrder(null)
            setAttempt(n => n + 1)
          }}
        >
          重试
        </button>
      </>
    )
  }
  else if (rows === null) {
    body = <Skeleton rows={3} />
  }
  else if (creating) {
    body = (
      <CreatePlaylistForm
        onCreated={(playlist) => {
          choose(playlist, true)
          backToList(`input[value="${CSS.escape(playlist.id)}"]`)
        }}
        onCancel={() => backToList('.save-playlist-new')}
      />
    )
  }
  else {
    body = (
      <>
        <p className="save-playlist-subject text-ui-caption muted truncate">{'tracks' in props ? `已选 ${props.tracks.length} 首` : props.title}</p>
        <ul ref={listRef} className="save-playlist-list">
          {rows.map(playlist => (
            <li key={playlist.id}>
              <label className="save-playlist-row">
                <input
                  type="checkbox"
                  value={playlist.id}
                  checked={(track ? included : picked).has(playlist.id)}
                  disabled={saving}
                  onChange={event => choose(playlist, event.target.checked)}
                />
                <PlaylistCover cover={playlist.coverUrl} favorites={playlist.favorites} size="sm" />
                <span className="save-playlist-name">
                  <span className="text-ui-base truncate">{playlist.name}</span>
                  {playlist.favorites && <FavoritesMark />}
                </span>
                <span className="text-ui-sm muted">{`${playlist.trackCount} 首`}</span>
              </label>
            </li>
          ))}
          <li>
            <button type="button" className="save-playlist-row save-playlist-new" disabled={saving} onClick={() => setCreating(true)}>
              <span className="save-playlist-new-tile"><Plus strokeWidth={1.7} aria-hidden="true" /></span>
              <span className="text-ui-base">新建歌单</span>
            </button>
          </li>
        </ul>
        {'tracks' in props && (
          <div className="dialog-actions">
            <button
              type="button"
              className="button-primary"
              disabled={saving || picked.size === 0}
              onClick={() => void save(props.from, props.tracks, rows.filter(p => picked.has(p.id)))}
            >
              保存
            </button>
          </div>
        )}
      </>
    )
  }
  return <Modal title={creating ? '新建歌单' : '保存到歌单'} onClose={onClose}>{body}</Modal>
}

export function SaveToPlaylistButton({ track, title, className = '' }: { track: Track, title: string, className?: string }) {
  // 记下打开时的歌：对话框开着时换了歌，勾选仍然作用于打开时的那首。
  const [target, setTarget] = useState<{ track: Track, title: string } | null>(null)
  return (
    <>
      <IconButton icon={ListPlus} className={className} label={saveToPlaylistLabel(title)} onClick={() => setTarget({ track, title })} />
      {target && <SaveToPlaylistDialog track={target.track} title={target.title} onClose={() => setTarget(null)} />}
    </>
  )
}
