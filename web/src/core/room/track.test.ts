import type { Track } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { sameTrack } from './track'

describe('sameTrack', () => {
  const track: Track = { source: 'qq', id: '1' }

  it('音源和 ID 都相同才是同一首歌', () => {
    expect(sameTrack(track, { source: 'qq', id: '1' })).toBe(true)
    expect(sameTrack(track, { source: 'qq', id: '2' })).toBe(false)
    expect(sameTrack(track, { source: 'netease', id: '1' })).toBe(false)
  })

  it('没有在放的歌时不相同', () => {
    expect(sameTrack(track, undefined)).toBe(false)
  })
})
