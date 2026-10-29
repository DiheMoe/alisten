import type { LucideIcon } from 'lucide-react'
import { formatDuration } from '@/shared/lib/time'
import { AsyncIconButton } from './icon-button'
import { SongArtwork } from './song-artwork'

export interface SongItem {
  key: string
  title: string
  artist: string
  album: string
  coverUrl: string
  // 0 表示未知。
  durationMs: number
  sourceUrl?: string
  // 显示在歌手后面，封面链接的提示也用它。
  sourceName?: string
  note?: string
  badge?: string
}

export interface SongAction {
  icon: LucideIcon
  label: string
  disabled?: boolean
  run: () => Promise<void>
}

export function SongList({ songs, actions }: { songs: SongItem[], actions: (song: SongItem, index: number) => SongAction[] }) {
  return (
    <ol className="song-list">
      {songs.map((song, index) => (
        <li className="song-row" key={song.key}>
          <span className="song-index text-ui-sm muted">{String(index + 1).padStart(2, '0')}</span>
          <SongArtwork coverUrl={song.coverUrl} title={song.title} href={song.sourceUrl} sourceName={song.sourceName} />
          <div className="song-copy">
            <span className="song-title">
              <strong className="text-ui-base truncate">{song.title}</strong>
              {song.badge && <span className="song-badge text-ui-xs">{song.badge}</span>}
            </span>
            <span className="text-ui-sm muted truncate">
              {[song.artist, song.sourceName, song.note].filter(Boolean).join(' · ')}
            </span>
          </div>
          <span className="song-album text-ui-sm muted truncate">{song.album}</span>
          <span className="song-duration text-ui-sm muted">{song.durationMs > 0 ? formatDuration(song.durationMs) : ''}</span>
          {actions(song, index).map(action => (
            <AsyncIconButton key={action.label} icon={action.icon} className="song-action" label={action.label} disabled={action.disabled} onAction={action.run} />
          ))}
        </li>
      ))}
    </ol>
  )
}
