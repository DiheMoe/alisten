import type { PlaybackSnapshot } from './timeline'
import { describe, expect, it } from 'vitest'
import { driftSeekPosition, startPlan } from './timeline'

function playback(positionMs: number, state: Partial<PlaybackSnapshot> = {}): PlaybackSnapshot {
  return { positionMs, paused: false, seeking: false, readyState: 4, ...state }
}

describe('startPlan', () => {
  it('还没到开始时间时等待', () => {
    expect(startPlan(-200, 180_000)).toEqual({ kind: 'wait', delayMs: 200 })
  })

  it('在时长之内时从应在位置开始', () => {
    expect(startPlan(0, 180_000)).toEqual({ kind: 'play', positionMs: 0 })
    expect(startPlan(42_000, 180_000)).toEqual({ kind: 'play', positionMs: 42_000 })
  })

  it('超过时长时这首歌已经结束', () => {
    expect(startPlan(180_000, 180_000)).toEqual({ kind: 'ended' })
  })
})

describe('driftSeekPosition', () => {
  it('偏差不超过 1 秒时不进行校正', () => {
    for (const actual of [10_149, 10_150, 11_000, 9851, 9850, 9000])
      expect(driftSeekPosition(playback(actual), 10_000)).toBeNull()
  })

  it('偏差超过 1 秒时返回定位位置', () => {
    expect(driftSeekPosition(playback(11_001), 10_000)).toBe(10_000)
    expect(driftSeekPosition(playback(8999), 10_000)).toBe(10_000)
  })

  it('没有足够数据时不进行校正', () => {
    for (const readyState of [0, 1, 2])
      expect(driftSeekPosition(playback(5001, { readyState }), 4000)).toBeNull()
    expect(driftSeekPosition(playback(5001, { readyState: 3 }), 4000)).toBe(4000)
  })

  it('暂停时不进行校正', () => {
    expect(driftSeekPosition(playback(5001, { paused: true }), 4000)).toBeNull()
  })

  it('正在定位时不进行校正', () => {
    expect(driftSeekPosition(playback(5001, { seeking: true }), 4000)).toBeNull()
  })
})
