import type { NowPlaying, Track } from '@/core/realtime'

export function sameTrack(a: Track, b: Track | undefined): boolean {
  return a.source === b?.source && a.id === b.id
}

export function playKey(nowPlaying: NowPlaying | null | undefined): string {
  return nowPlaying ? `${nowPlaying.entry.id}@${nowPlaying.startedAtMs}` : ''
}

export function sourceName(track: Track): string | undefined {
  return track.source === 'bilibili' ? '哔哩哔哩' : undefined
}
