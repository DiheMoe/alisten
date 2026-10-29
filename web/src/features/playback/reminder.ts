import type { NowPlaying } from '@/core/realtime'

// 点完很快就开播的歌不提醒，否则「已点歌」和「轮到你点的」会一起出现。
const minimumWaitMs = 30_000

export function shouldRemind(previous: NowPlaying | null, next: NowPlaying, memberId: string, live: boolean, enabled: boolean, waitedMs: number | null): boolean {
  return enabled && live && next.entry.by?.id === memberId && next.entry.id !== previous?.entry.id
    && waitedMs !== null && waitedMs >= minimumWaitMs
}
