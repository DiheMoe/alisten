import type { Track } from '@/core/realtime'

export function sameTrack(a: Track, b: Track | undefined): boolean {
  return a.source === b?.source && a.id === b.id
}
