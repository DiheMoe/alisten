import type { StoredMedia } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { deleteConfirmText, formatPlayed, formatSize, formatStoredDate, mediaByline, mediaOrigin, removalMessage, searchDraft } from './media-view'

const MiB = 1024 * 1024

function media(patch: Partial<StoredMedia>): StoredMedia {
  return { key: 'k', source: 'link', link: 'https://example.com/a.mp3', title: '夜に駆ける', artist: '', coverUrl: '', size: 0, storedAtMs: 0, lastPlayedAtMs: null, ...patch }
}

describe('formatSize', () => {
  it('不到 1 GiB 用 MiB，之后用 GiB，保留一位小数', () => {
    expect(formatSize(0)).toBe('0.0 MiB')
    expect(formatSize(8.94 * MiB)).toBe('8.9 MiB')
    expect(formatSize(1023 * MiB)).toBe('1023.0 MiB')
    expect(formatSize(1024 * MiB)).toBe('1.0 GiB')
    expect(formatSize(9.6 * 1024 * MiB)).toBe('9.6 GiB')
  })
})

describe('formatStoredDate', () => {
  const now = new Date(2026, 10, 11, 20, 0).getTime()

  it('今年的只写月日，往年的带上年份', () => {
    expect(formatStoredDate(new Date(2026, 2, 2, 8, 30).getTime(), now)).toBe('3 月 2 日')
    expect(formatStoredDate(new Date(2025, 11, 31, 23, 59).getTime(), now)).toBe('2025 年 12 月 31 日')
  })
})

describe('formatPlayed', () => {
  const now = new Date(2026, 10, 11, 20, 0).getTime()
  const minute = 60 * 1000
  const day = 24 * 60 * minute

  it('没有播放过', () => {
    expect(formatPlayed(null, now)).toBe('从未播放')
  })

  it('按经过的时间取最大的单位', () => {
    expect(formatPlayed(now - 59 * 1000, now)).toBe('刚刚')
    expect(formatPlayed(now - 5 * minute, now)).toBe('5 分钟前')
    expect(formatPlayed(now - 3 * 60 * minute, now)).toBe('3 小时前')
    expect(formatPlayed(now - day, now)).toBe('1 天前')
    expect(formatPlayed(now - 29 * day, now)).toBe('29 天前')
    expect(formatPlayed(now - 125 * day, now)).toBe('4 个月前')
    expect(formatPlayed(now - 400 * day, now)).toBe('1 年前')
  })

  it('服务器的时钟比本机快时算作刚刚', () => {
    expect(formatPlayed(now + 5 * minute, now)).toBe('刚刚')
  })
})

describe('mediaOrigin', () => {
  it('哔哩哔哩的歌写平台名，链接写网站的域名', () => {
    expect(mediaOrigin(media({ source: 'bilibili', link: 'https://www.bilibili.com/video/BV1xx411c7mD' }))).toBe('哔哩哔哩')
    expect(mediaOrigin(media({ source: 'link', link: 'https://music.example.com/a/b.mp3?x=1' }))).toBe('music.example.com')
  })

  it('解析不了的链接原样显示', () => {
    expect(mediaOrigin(media({ source: 'link', link: 'not a url' }))).toBe('not a url')
  })
})

describe('mediaByline', () => {
  it('有歌手时写“歌手 · 来源”', () => {
    expect(mediaByline(media({ artist: 'YOASOBI', source: 'link', link: 'https://music.example.com/a.mp3' }))).toBe('YOASOBI · music.example.com')
  })

  it('不知道歌手时只写来源', () => {
    expect(mediaByline(media({ source: 'bilibili', link: 'https://www.bilibili.com/video/BV1xx411c7mD' }))).toBe('哔哩哔哩')
  })
})

describe('removalMessage', () => {
  it('全部删除', () => {
    expect(removalMessage(3, 0)).toBe('已删除 3 首')
  })

  it('有正在播放的歌时补一句', () => {
    expect(removalMessage(3, 1)).toBe('已删除 2 首；1 首正在房间里播放，没有删除')
    expect(removalMessage(2, 2)).toBe('2 首正在房间里播放，没有删除')
  })
})

describe('deleteConfirmText', () => {
  it('一首写出歌名和大小', () => {
    expect(deleteConfirmText([media({ size: 8.94 * MiB })])).toBe('删除《夜に駆ける》（8.9 MiB）的音频和封面后，再点这首歌会重新下载。')
  })

  it('多首写出数量和总大小', () => {
    expect(deleteConfirmText([media({ size: 8.94 * MiB }), media({ key: 'k2', size: 7.4 * MiB }), media({ key: 'k3', size: 5.4 * MiB })]))
      .toBe('删除选中的 3 首歌（共 21.7 MiB）的音频和封面后，再点这些歌会重新下载。')
  })
})

describe('searchDraft', () => {
  it('地址里的搜索词是输入框自己提交的值时，保留输入框里的原样输入，包括首尾的空白', () => {
    expect(searchDraft('summer ', 'summer')).toBe('summer ')
    expect(searchDraft('  lemon', 'lemon')).toBe('  lemon')
    expect(searchDraft('summer', 'summer')).toBe('summer')
  })

  it('地址里的搜索词被别处改掉时（清除搜索、后退），输入框跟着改', () => {
    expect(searchDraft('summer ', '')).toBe('')
    expect(searchDraft('summer breeze', 'summer')).toBe('summer')
    expect(searchDraft('  ', 'lemon')).toBe('lemon')
  })
})
