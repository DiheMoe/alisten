const seekThresholdMs = 1000
const haveFutureData = 3
const lateStartLimitMs = 8000

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

// 返回这首歌要比房间落后多少毫秒。加载好时开播还不到 lateStartLimitMs，就从头放，这首歌里一直落后这么多。这样网络慢的客户端也听得到开头，代价是结尾会在服务端切歌时被截掉。来得更晚的，比如中途进房间，不落后，直接从应在的位置放。
export function startLag(elapsedMs: number): number {
  return elapsedMs <= lateStartLimitMs ? Math.max(0, elapsedMs) : 0
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
