import type { PlaylistSort } from './playlist-sort'
import type { MyPlaylist, Track } from '@/core/realtime'
import { ListPlus, Plus } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { profileStore } from '@/features/settings'
import { useStore } from '@/shared/lib/use-store'
import { IconButton, Modal, Skeleton } from '@/shared/ui'
import { openAccountSettings } from './account-settings'
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

export function SaveToPlaylistDialog({ track, title, onClose }: { track: Track, title: string, onClose: () => void }) {
  const account = useStore(profileStore, s => s.account)
  const playlists = useStore(playlistsStore, s => s.playlists)
  const error = useStore(playlistsStore, s => s.error)
  const included = useStore(playlistsStore, s => s.memberships.get(trackKey(track))) ?? none
  const [order, setOrder] = useState<string[] | null>(null)
  const [readFailed, setReadFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [creating, setCreating] = useState(false)
  const listRef = useRef<HTMLUListElement>(null)
  // 从新建页回到列表时要获得焦点的元素：取消时是“新建歌单”，建好之后是新歌单的勾选框，聚焦的同时把它滚进视野。
  const returnFocusRef = useRef<string | null>(null)
  // 打开时刷新歌单：歌曲数和其他设备上新建的歌单。
  useEffect(() => {
    if (!account)
      return
    let active = true
    void Promise.all([playlistsStore.load(), playlistsStore.containing(track)]).then(([, read]) => {
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

  const rows = playlists && order ? arrange(playlists, order) : null

  const choose = (playlist: MyPlaylist, checked: boolean) => {
    void playlistsStore.setMembership(playlist, track, checked)
  }

  const backToList = (focus: string) => {
    returnFocusRef.current = focus
    setCreating(false)
  }

  let body
  if (!account) {
    body = (
      <>
        <p className="text-ui-base muted">注册或登录之后就能把歌保存到歌单。</p>
        <div className="dialog-actions">
          <button
            type="button"
            className="button-primary"
            onClick={() => {
              onClose()
              openAccountSettings()
            }}
          >
            去账号设置
          </button>
        </div>
      </>
    )
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
        <p className="save-playlist-subject text-ui-caption muted truncate">{title}</p>
        <ul ref={listRef} className="save-playlist-list">
          {rows.map(playlist => (
            <li key={playlist.id}>
              <label className="save-playlist-row">
                <input
                  type="checkbox"
                  value={playlist.id}
                  checked={included.has(playlist.id)}
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
            <button type="button" className="save-playlist-row save-playlist-new" onClick={() => setCreating(true)}>
              <span className="save-playlist-new-tile"><Plus strokeWidth={1.7} aria-hidden="true" /></span>
              <span className="text-ui-base">新建歌单</span>
            </button>
          </li>
        </ul>
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
