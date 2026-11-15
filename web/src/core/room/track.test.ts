import type { NowPlaying, Track } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { playKey, sameTrack } from './track'

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

describe('playKey', () => {
  function nowPlaying(entryId: string, startedAtMs: number): NowPlaying {
    return { entry: { id: entryId }, startedAtMs } as NowPlaying
  }

  it('条目和开播时间都相同才是同一次播放', () => {
    expect(playKey(nowPlaying('1', 1000))).toBe(playKey(nowPlaying('1', 1000)))
    expect(playKey(nowPlaying('1', 1000))).not.toBe(playKey(nowPlaying('2', 1000)))
    expect(playKey(nowPlaying('1', 1000))).not.toBe(playKey(nowPlaying('1', 2000)))
  })

  it('没有在放的歌时是空串，和任何一次播放都不同', () => {
    expect(playKey(null)).toBe('')
    expect(playKey(undefined)).toBe('')
    expect(playKey(nowPlaying('', 0))).not.toBe('')
  })
})
