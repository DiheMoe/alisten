const seekThresholdMs = 1000
const haveFutureData = 3

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
