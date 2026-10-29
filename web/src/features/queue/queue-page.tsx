import type { Entry, PlayMode } from '@/core/realtime'
import type { SongItem } from '@/shared/ui'
import { Heart, ListMusic, Trash2 } from 'lucide-react'
import { roomStore } from '@/core/room'
import { bookTitle } from '@/shared/lib/book-title'
import { useStore } from '@/shared/lib/use-store'
import { EmptyState, SongArtwork, SongList } from '@/shared/ui'
import { likeEntry, removeEntry, setPlayMode } from './actions'
import { sourceName } from './source'
import './queue.css'

const noEntries: Entry[] = []

function songItem(entry: Entry): SongItem {
  return {
    key: entry.id,
    title: entry.meta.title,
    artist: entry.meta.artist || '未知艺术家',
    album: entry.meta.album || '未知专辑',
    coverUrl: entry.meta.coverUrl,
    durationMs: entry.meta.durationMs,
    sourceUrl: entry.meta.pageUrl,
    sourceName: sourceName(entry.track),
    note: entry.by ? `${entry.by.name} 点的歌` : undefined,
    badge: entry.status === 'preparing' ? '准备中' : undefined,
  }
}

function QueueCard({ label, entry, className = '' }: { label: string, entry: Entry, className?: string }) {
  const song = songItem(entry)
  return (
    <div className={`queue-current ${className}`}>
      <SongArtwork coverUrl={song.coverUrl} title={song.title} href={song.sourceUrl} sourceName={song.sourceName} />
      <div className="song-copy">
        <span className="text-ui-xs accent">{song.badge ? `${label} · ${song.badge}` : label}</span>
        <strong className="text-ui-base truncate">{song.title}</strong>
        <span className="text-ui-sm muted truncate">{song.sourceName ? `${song.artist} · ${song.sourceName}` : song.artist}</span>
      </div>
    </div>
  )
}

export function QueuePage({ compact = false }: { compact?: boolean }) {
  const nowPlaying = useStore(roomStore, s => s.room?.nowPlaying ?? null)
  const pending = useStore(roomStore, s => s.room?.pending ?? null)
  const items = useStore(roomStore, s => s.room?.items ?? noEntries)
  const playMode = useStore(roomStore, s => s.room?.playMode ?? 'sequential')
  const connected = useStore(roomStore, s => s.status === 'open')
  return (
    <section className={compact ? 'queue-panel' : 'page-content queue-page'} aria-label="播放列表">
      <div className="section-heading">
        <h1 className={compact ? 'text-ui-base' : 'text-ui-xl'}>
          播放列表
          <span className="text-ui-sm muted">{items.length}</span>
        </h1>
        <select className="queue-mode" aria-label="播放顺序" value={playMode} disabled={!connected} onChange={event => void setPlayMode(event.target.value as PlayMode)}>
          <option value="sequential">顺序播放</option>
          <option value="rotation" title="按点歌人轮流播放">轮流播放</option>
          <option value="random">随机播放</option>
        </select>
      </div>
      {nowPlaying && <QueueCard label="正在播放" entry={nowPlaying.entry} />}
      {pending && <QueueCard label="下一首" entry={pending} className="queue-pending" />}
      {items.length
        ? (
            <SongList
              songs={items.map(songItem)}
              actions={song => [
                { icon: Heart, label: `点赞${bookTitle(song.title)}`, disabled: !connected, run: () => likeEntry(song.key, song.title) },
                { icon: Trash2, label: `移除${bookTitle(song.title)}`, disabled: !connected, run: () => removeEntry(song.key, song.title) },
              ]}
            />
          )
        : !pending && (
            <EmptyState icon={ListMusic} title={nowPlaying ? '后面还没有歌' : '播放列表还是空的'}>
              每个人都可以点歌，一起决定下一首。
            </EmptyState>
          )}
    </section>
  )
}
