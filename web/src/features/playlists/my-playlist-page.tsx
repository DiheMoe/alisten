import type { MyPlaylistItem, Track } from '@/core/realtime'
import type { SongItem } from '@/shared/ui'
import { ArrowLeft, CirclePlus, ListMusic, ListPlus, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { roomStore, sameTrack } from '@/core/room'
import { enqueue, sourceName } from '@/features/queue'
import { bookTitle } from '@/shared/lib/book-title'
import { useStore } from '@/shared/lib/use-store'
import { EmptyState, Skeleton, SongList } from '@/shared/ui'
import { FavoritesMark, PlaylistCover } from './playlist-cover'
import { formatPlaylistDate } from './playlist-date'
import { PlaylistMenu } from './playlist-menu'
import { playlistsStore, trackKey } from './playlists-store'
import { saveToPlaylistLabel } from './save-label'
import { SaveToPlaylistDialog } from './save-to-playlist'
import './playlists.css'

export function MyPlaylistPage({ id, onBack }: { id: string, onBack: () => void }) {
  const playlist = useStore(playlistsStore, s => s.playlists?.find(p => p.id === id) ?? null)
  const listed = useStore(playlistsStore, s => s.playlists !== null)
  const connected = useStore(roomStore, s => s.status === 'open')
  const ephemeral = useStore(roomStore, s => s.room?.room.kind === 'ephemeral')
  const playing = useStore(roomStore, s => s.room?.nowPlaying?.entry.track)
  const [items, setItems] = useState<MyPlaylistItem[] | null>(null)
  const [failed, setFailed] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState<{ track: Track, title: string } | null>(null)
  // 歌单的更新时间变了就重新读取：加入、移出、移动和复制都会推进它，歌曲数不变时顺序也可能变了。
  useEffect(() => {
    let active = true
    api.listMyPlaylistItems(id).then(({ items }) => {
      if (!active)
        return
      setItems(items)
      setFailed(null)
    }, (error: unknown) => {
      if (active)
        setFailed(errorMessage(error))
    })
    return () => {
      active = false
    }
  }, [id, attempt, playlist?.updatedAtMs])
  useEffect(() => {
    const refresh = () => setAttempt(n => n + 1)
    window.addEventListener('focus', refresh)
    return () => window.removeEventListener('focus', refresh)
  }, [])
  const now = useStore(playlistsStore, s => s.loadedAt)
  // 读到了自己的歌单，里面却没有它：在别处删掉了，或者换了账号。
  const gone = listed && !playlist
  // 读不到时不再显示之前的曲目：它们可能已经不在了，也可能属于之前的账号。
  const shown = gone || failed ? null : items
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
    body = (
      <SongList
        songs={items.map(({ track, meta }): SongItem => ({
          key: trackKey(track),
          title: meta.title,
          artist: meta.artist || '未知艺术家',
          album: meta.album,
          coverUrl: meta.coverUrl,
          durationMs: meta.durationMs,
          sourceUrl: meta.pageUrl,
          sourceName: sourceName(track),
          playing: sameTrack(track, playing),
        }))}
        actions={(song, index) => {
          const { track } = items[index]
          const blocked = track.source === 'link' && ephemeral
          return [
            {
              icon: CirclePlus,
              label: blocked ? '临时房间不能点链接里的歌' : `点歌${bookTitle(song.title)}`,
              disabled: !connected || blocked,
              run: async () => {
                await enqueue(track)
              },
            },
            {
              icon: ListPlus,
              label: saveToPlaylistLabel(song.title),
              secondary: true,
              run: async () => setSaving({ track, title: song.title }),
            },
            {
              icon: Trash2,
              label: `把${bookTitle(song.title)}移出这个歌单`,
              disabled: !playlist,
              secondary: true,
              run: async () => {
                if (!playlist)
                  return
                // 失败时歌单可能已经在别处变了，重新读取。
                if (await playlistsStore.removeItems(playlist, [track]))
                  setItems(list => list?.filter(item => !sameTrack(item.track, track)) ?? list)
                else
                  setAttempt(n => n + 1)
              },
            },
          ]
        }}
      />
    )
  }
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
          {playlist && !playlist.favorites && <PlaylistMenu playlist={playlist} align="start" className="my-playlist-menu" onDeleted={onBack} />}
        </div>
      </header>
      {body}
      {!connected && shown?.length ? <p className="text-ui-caption muted my-playlist-hint">进入房间之后才能点歌。</p> : null}
      {saving && <SaveToPlaylistDialog track={saving.track} title={saving.title} onClose={() => setSaving(null)} />}
    </section>
  )
}
