import { describe, expect, it } from 'vitest'
import { formatPlaylistDate, playlistDates } from './playlist-date'

// 用本地时间构造时刻，这样断言与运行测试的时区无关。
function at(year: number, month: number, day: number, hour = 0, minute = 0): number {
  return new Date(year, month - 1, day, hour, minute).getTime()
}

const now = at(2026, 11, 9, 15, 30)

describe('formatPlaylistDate', () => {
  it('今天的显示时刻', () => {
    expect(formatPlaylistDate(at(2026, 11, 9, 14, 20), now)).toBe('今天 14:20')
    expect(formatPlaylistDate(at(2026, 11, 9, 0, 5), now)).toBe('今天 00:05')
  })

  it('昨天的显示时刻', () => {
    expect(formatPlaylistDate(at(2026, 11, 8, 21, 4), now)).toBe('昨天 21:04')
    expect(formatPlaylistDate(at(2026, 11, 8, 0, 0), at(2026, 11, 9, 23, 59))).toBe('昨天 00:00')
  })

  it('跨月的昨天', () => {
    expect(formatPlaylistDate(at(2026, 10, 31, 23, 59), at(2026, 11, 1, 0, 1))).toBe('昨天 23:59')
  })

  it('同一年更早的只显示月日', () => {
    expect(formatPlaylistDate(at(2026, 11, 7, 9, 0), now)).toBe('11月7日')
    expect(formatPlaylistDate(at(2026, 1, 1, 0, 0), now)).toBe('1月1日')
  })

  it('跨年显示年份', () => {
    expect(formatPlaylistDate(at(2025, 12, 31, 22, 0), now)).toBe('2025年12月31日')
    expect(formatPlaylistDate(at(2025, 12, 31, 22, 0), at(2026, 1, 1, 8, 0))).toBe('昨天 22:00')
  })
})

describe('playlistDates', () => {
  it('更新时间和创建时间显示得不一样时都列出来', () => {
    expect(playlistDates(at(2026, 11, 9, 14, 20), at(2026, 11, 7, 9, 0), now)).toBe('更新于今天 14:20，创建于11月7日')
  })

  it('显示得一样时只列创建时间', () => {
    expect(playlistDates(at(2026, 11, 9, 14, 20), at(2026, 11, 9, 14, 20), now)).toBe('创建于今天 14:20')
    expect(playlistDates(at(2026, 11, 7, 18, 0), at(2026, 11, 7, 9, 0), now)).toBe('创建于11月7日')
  })
})
