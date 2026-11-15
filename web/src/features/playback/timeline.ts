import type { NextTrack, NowPlaying } from '@/core/realtime'

const seekThresholdMs = 1000
export const haveFutureData = 3

export type StartPlan
  = | { kind: 'wait', delayMs: number }
    | { kind: 'play', positionMs: number }
    | { kind: 'ended' }

export function startPlan(expectedMs: number, durationMs: number): StartPlan {
  if (expectedMs < 0)
    return { kind: 'wait', delayMs: -expectedMs }
  if (expectedMs >= durationMs)
    return { kind: 'ended' }
  return { kind: 'play', positionMs: expectedMs }
}

export interface PlaybackSnapshot {
  positionMs: number
  paused: boolean
  seeking: boolean
  readyState: number
}

export function driftSeekPosition(snapshot: PlaybackSnapshot, expectedMs: number): number | null {
  if (snapshot.paused || snapshot.seeking || snapshot.readyState < haveFutureData)
    return null
  if (Math.abs(snapshot.positionMs - expectedMs) <= seekThresholdMs)
    return null
  return expectedMs
}

export type PreloadPlan
  = | { kind: 'load', entryId: string, url: string }
    | { kind: 'wait', delayMs: number }
    | { kind: 'none' }

// loadWithinMs 不为 null 时播放地址有时效，只能在当前这首结束前这么久之内加载，所以要等时钟校准之后才能算出从什么时候开始。
export function preloadPlan(next: NextTrack | null, current: NowPlaying | null, nowMs: number | null): PreloadPlan {
  if (!next || !current)
    return { kind: 'none' }
  // 开播事件先到、队列事件还没到时，下一首还是刚开播的这首。这时不能再加载它，否则会把同一首再请求一遍。
  if (next.entryId === current.entry.id)
    return { kind: 'none' }
  const load: PreloadPlan = { kind: 'load', entryId: next.entryId, url: next.mediaUrl }
  if (next.loadWithinMs === null)
    return load
  if (nowMs === null)
    return { kind: 'none' }
  const fromMs = current.startedAtMs + current.durationMs - next.loadWithinMs
  return nowMs < fromMs ? { kind: 'wait', delayMs: fromMs - nowMs } : load
}
