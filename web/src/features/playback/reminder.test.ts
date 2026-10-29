import type { Member, NowPlaying } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { shouldRemind } from './reminder'

const me: Member = { id: 'm1', name: '小明', avatarHash: '' }
const other: Member = { id: 'm2', name: '阿青', avatarHash: '' }
const bot: Member = { id: 'm3', name: '点歌机器人', avatarHash: '' }

const live = true
const snapshot = false
const on = true
const off = false
const waited = 60_000

function playing(id: string, by: Member | null, likes = 0): NowPlaying {
  return {
    durationMs: 1000,
    entry: {
      id,
      track: { source: 'netease', id },
      meta: { title: id, artist: '', album: '', coverUrl: '', pageUrl: '', durationMs: 1000 },
      by,
      auto: by === null,
      likes,
      status: 'ready',
    },
    gain: 1,
    lyric: '',
    mediaUrl: `/media/${id}`,
    startedAtMs: 0,
  }
}

describe('shouldRemind', () => {
  it('实时事件开始播放自己点的歌时提醒', () => {
    expect(shouldRemind(null, playing('1', me), me.id, live, on, waited)).toBe(true)
    expect(shouldRemind(playing('1', other), playing('2', me), me.id, live, on, waited)).toBe(true)
  })

  it('快照里正在播放自己点的歌时不提醒', () => {
    expect(shouldRemind(null, playing('1', me), me.id, snapshot, on, waited)).toBe(false)
    expect(shouldRemind(playing('1', other), playing('2', me), me.id, snapshot, on, waited)).toBe(false)
  })

  it('同一首歌的后续更新不再提醒', () => {
    expect(shouldRemind(playing('1', me), playing('1', me, 1), me.id, live, on, waited)).toBe(false)
  })

  it('别人、机器人和 AutoDJ 点的歌不提醒', () => {
    for (const by of [other, bot, null])
      expect(shouldRemind(null, playing('1', by), me.id, live, on, waited)).toBe(false)
  })

  it('关掉提醒后不提醒', () => {
    expect(shouldRemind(playing('1', other), playing('2', me), me.id, live, off, waited)).toBe(false)
  })

  it('点完不到 30 秒就开播的歌不提醒', () => {
    expect(shouldRemind(playing('1', other), playing('2', me), me.id, live, on, 29_999)).toBe(false)
  })

  it('等满 30 秒才开播的歌提醒', () => {
    expect(shouldRemind(playing('1', other), playing('2', me), me.id, live, on, 30_000)).toBe(true)
  })

  it('开播前没见过的歌不提醒', () => {
    expect(shouldRemind(playing('1', other), playing('2', me), me.id, live, on, null)).toBe(false)
  })
})
