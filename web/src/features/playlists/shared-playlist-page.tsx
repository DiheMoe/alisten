import type { MyPlaylistItem, SharedPlaylist, Track } from '@/core/realtime'
import type { SongAction, SongItem } from '@/shared/ui'
import { CirclePlus, Link2Off, ListMusic, ListPlus } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api, ApiError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { roomStore, sameTrack } from '@/core/room'
import { enqueue, sourceName } from '@/features/queue'
import { bookTitle } from '@/shared/lib/book-title'
import { useStore } from '@/shared/lib/use-store'
import { Avatar, EmptyState, Skeleton, SongList } from '@/shared/ui'
import { CopySharedPlaylistButton } from './copy-shared-playlist'
import { FavoritesMark, PlaylistCover } from './playlist-cover'
import { trackKey } from './playlists-store'
import { saveToPlaylistLabel } from './save-label'
import { SaveToPlaylistDialog } from './save-to-playlist'
import './playlists.css'

// 带上读取的分享 ID：换到另一个分享时，上一个的结果不再显示。
type Result = { shareId: string } & ({ kind: 'loaded', playlist: SharedPlaylist } | { kind: 'gone' } | { kind: 'failed', message: string })

function songItem({ track, meta }: MyPlaylistItem, playing: Track | undefined): SongItem {
  return {
    key: trackKey(track),
    title: meta.title,
    artist: meta.artist || '未知艺术家',
    album: meta.album,
    coverUrl: meta.coverUrl,
    durationMs: meta.durationMs,
    sourceUrl: meta.pageUrl,
    sourceName: sourceName(track),
    playing: sameTrack(track, playing),
  }
}

export function SharedPlaylistPage({ shareId }: { shareId: string }) {
  const connected = useStore(roomStore, s => s.status === 'open')
  const ephemeral = useStore(roomStore, s => s.room?.room.kind === 'ephemeral')
  const playing = useStore(roomStore, s => s.room?.nowPlaying?.entry.track)
  const [result, setResult] = useState<Result | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState<{ track: Track, title: string } | null>(null)
  useEffect(() => {
    let active = true
    api.getSharedPlaylist(shareId).then((playlist) => {
      if (active)
        setResult({ shareId, kind: 'loaded', playlist })
    }, (error: unknown) => {
      if (!active)
        return
      setResult(error instanceof ApiError && error.code === 'NOT_FOUND'
        ? { shareId, kind: 'gone' }
        : { shareId, kind: 'failed', message: errorMessage(error) })
    })
    return () => {
      active = false
    }
  }, [shareId, attempt])

  const current = result?.shareId === shareId ? result : null
  if (!current) {
    return <section className="page-content my-playlist-page"><Skeleton rows={6} /></section>
  }
  if (current.kind === 'gone') {
    return (
      <section className="page-content my-playlist-page">
        <EmptyState icon={Link2Off} title="分享已失效">
          歌单被删除了，或者分享的人停止了分享。
        </EmptyState>
      </section>
    )
  }
  if (current.kind === 'failed') {
    return (
      <section className="page-content my-playlist-page">
        <EmptyState
          icon={ListMusic}
          title={current.message}
          action={(
            <button
              type="button"
              className="button-secondary"
              onClick={() => {
                setResult(null)
                setAttempt(n => n + 1)
              }}
            >
              重试
            </button>
          )}
        />
      </section>
    )
  }

  const { playlist } = current
  const { items } = playlist
  const actions = (_: SongItem, index: number): SongAction[] => {
    const { track, meta } = items[index]
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
    ]
  }
  return (
    <section className="page-content my-playlist-page" aria-labelledby="shared-playlist-title">
      <header className="my-playlist-header">
        <PlaylistCover cover={items.find(item => item.meta.coverUrl)?.meta.coverUrl ?? null} favorites={playlist.favorites} size="lg" />
        <div className="my-playlist-info">
          <h1 id="shared-playlist-title" className="my-playlist-name">
            <span>{playlist.name}</span>
            {playlist.favorites && <FavoritesMark size={22} />}
          </h1>
          <p className="shared-playlist-owner text-ui-base muted">
            <Avatar hash={playlist.ownerAvatarHash} name={playlist.ownerName} className="text-ui-xs" />
            <span className="truncate">{`${playlist.ownerName} 分享，${items.length} 首`}</span>
          </p>
          <div className="my-playlist-actions">
            <CopySharedPlaylistButton shareId={shareId} name={playlist.name} />
          </div>
        </div>
      </header>
      {items.length === 0
        ? <EmptyState icon={ListMusic} title="这个歌单还是空的" />
        : <SongList songs={items.map(item => songItem(item, playing))} actions={actions} />}
      {!connected && items.length > 0 && <p className="text-ui-caption muted my-playlist-hint">进入房间之后才能点歌。</p>}
      {saving && <SaveToPlaylistDialog track={saving.track} title={saving.title} onClose={() => setSaving(null)} />}
    </section>
  )
}
