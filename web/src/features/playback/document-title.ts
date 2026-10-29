import type { TrackMeta } from '@/core/realtime'

export function documentTitle(brandName: string, playing: Pick<TrackMeta, 'title' | 'artist'> | null): string {
  if (!playing)
    return brandName
  return `${[playing.title, playing.artist].filter(Boolean).join(' - ')} | ${brandName}`
}
