import type { MyPlaylistItem, SharedPlaylist, Track } from '@/core/realtime'
import type { SongAction, SongItem } from '@/shared/ui'
import { CirclePlus, Link2Off, ListMusic, ListPlus } from 'lucide-react'
import { useState } from 'react'
import { api, ApiError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { enqueue, pickButton, roomStore, sameTrack, sourceName } from '@/core/room'
import { useResource } from '@/shared/lib/use-resource'
import { useStore } from '@/shared/lib/use-store'
import { Avatar, EmptyState, Skeleton, SongList } from '@/shared/ui'
import { CopySharedPlaylistButton } from './copy-shared-playlist'
import { FavoritesMark, PlaylistCover } from './playlist-cover'
import { trackKey } from './playlists-store'
import { saveToPlaylistLabel } from './save-label'
import { SaveToPlaylistDialog } from './save-to-playlist'
import './playlists.css'

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
  // 分享已失效时读到 null。
  const shared = useResource((): Promise<SharedPlaylist | null> => api.getSharedPlaylist(shareId).catch((error: unknown) => {
    if (error instanceof ApiError && error.code === 'NOT_FOUND')
      return null
    throw new Error(errorMessage(error))
  }), shareId)
  const [saving, setSaving] = useState<{ track: Track, title: string } | null>(null)

  if (shared.state.status === 'loading') {
    return <section className="page-content my-playlist-page"><Skeleton rows={6} /></section>
  }
  if (shared.state.status === 'failed') {
    return (
      <section className="page-content my-playlist-page">
        <EmptyState
          icon={ListMusic}
          title={shared.state.error}
          action={<button type="button" className="button-secondary" onClick={shared.reload}>重试</button>}
        />
      </section>
    )
  }
  const playlist = shared.state.value
  if (!playlist) {
    return (
      <section className="page-content my-playlist-page">
        <EmptyState icon={Link2Off} title="分享已失效">
          歌单被删除了，或者分享的人停止了分享。
        </EmptyState>
      </section>
    )
  }

  const { items } = playlist
  const actions = (_: SongItem, index: number): SongAction[] => {
    const { track, meta } = items[index]
    return [
      {
        icon: CirclePlus,
        ...pickButton(track, meta.title, { connected, ephemeral }),
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
