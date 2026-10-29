import type { Track } from '@/core/realtime'

export function sourceName(track: Track): string | undefined {
  return track.source === 'bilibili' ? '哔哩哔哩' : undefined
}
