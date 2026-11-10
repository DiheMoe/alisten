import type { MyPlaylistItem } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { exportFileName, exportPlaylist, importLimit, parsePlaylistFile } from './playlist-file'

const sunny: MyPlaylistItem = {
  track: { source: 'netease', id: '186016' },
  meta: { title: '晴天', artist: '周杰伦', album: '叶惠美', coverUrl: 'https://p1.music.126.net/a.jpg', pageUrl: 'https://music.163.com/#/song?id=186016', durationMs: 269000 },
}
const clip: MyPlaylistItem = {
  track: { source: 'link', id: 'https://example.com/a.mp3' },
  meta: { title: 'a.mp3', artist: '', album: '', coverUrl: '', pageUrl: 'https://example.com/a.mp3', durationMs: 0 },
}

function file(tracks: unknown, extra: Record<string, unknown> = {}): string {
  return JSON.stringify({ format: 'alisten.playlist', name: '通勤', exportedAt: '2026-11-09T12:00:00.000Z', tracks, ...extra })
}

describe('exportPlaylist', () => {
  it('按设计的格式导出，曲目保持歌单里的先后', () => {
    const exported = JSON.parse(exportPlaylist('通勤', [sunny, clip], Date.UTC(2026, 10, 9, 12)))
    expect(exported).toEqual({
      format: 'alisten.playlist',
      name: '通勤',
      exportedAt: '2026-11-09T12:00:00.000Z',
      tracks: [
        { source: 'netease', id: '186016', title: '晴天', artist: '周杰伦', album: '叶惠美', durationMs: 269000, coverUrl: 'https://p1.music.126.net/a.jpg', pageUrl: 'https://music.163.com/#/song?id=186016' },
        { source: 'link', id: 'https://example.com/a.mp3', title: 'a.mp3', artist: '', album: '', durationMs: 0, coverUrl: '', pageUrl: 'https://example.com/a.mp3' },
      ],
    })
  })

  it('导出的文件能原样读回', () => {
    expect(parsePlaylistFile(exportPlaylist('通勤', [sunny, clip], 0))).toEqual({
      name: '通勤',
      truncated: false,
      tracks: [
        { track: sunny.track, title: '晴天', artist: '周杰伦', album: '叶惠美', durationMs: 269000, coverUrl: 'https://p1.music.126.net/a.jpg', pageUrl: 'https://music.163.com/#/song?id=186016' },
        { track: clip.track, title: 'a.mp3', artist: '', album: '', durationMs: 0, coverUrl: '', pageUrl: 'https://example.com/a.mp3' },
      ],
    })
  })
})

describe('exportFileName', () => {
  it('用歌单名作文件名', () => {
    expect(exportFileName('我喜欢')).toBe('我喜欢.json')
  })

  it('替换文件名里不能用的字符', () => {
    expect(exportFileName('a/b\\c:d*e?f"g<h>i|j\tk\u007Fl')).toBe('a_b_c_d_e_f_g_h_i_j_k_l.json')
  })
})

describe('parsePlaylistFile', () => {
  const notPlaylistFile = '这个文件不是导出的歌单。'
  const cases: { name: string, content: string, want: string }[] = [
    { name: '不是 JSON', content: '晴天', want: notPlaylistFile },
    { name: '不是对象', content: '[]', want: notPlaylistFile },
    { name: '没有 format', content: JSON.stringify({ name: '通勤', tracks: [] }), want: notPlaylistFile },
    { name: 'format 不对', content: file([], { format: 'alisten.history' }), want: notPlaylistFile },
    { name: 'tracks 不是数组', content: file({ 0: sunny.track }), want: notPlaylistFile },
    { name: '空数组', content: file([]), want: '这个歌单文件里没有歌。' },
    { name: '缺少音源', content: file([{ id: '186016', title: '晴天' }]), want: '文件里的第 1 首歌缺少音源或 ID，没有导入。' },
    { name: '音源不认识', content: file([{ source: 'netease', id: '1' }, { source: 'spotify', id: '2' }]), want: '文件里的第 2 首歌缺少音源或 ID，没有导入。' },
    { name: '音源是原型上的属性名', content: file([{ source: 'toString', id: '1' }]), want: '文件里的第 1 首歌缺少音源或 ID，没有导入。' },
    { name: 'ID 为空', content: file([{ source: 'qq', id: '' }]), want: '文件里的第 1 首歌缺少音源或 ID，没有导入。' },
    { name: 'ID 不是字符串', content: file([{ source: 'netease', id: 186016 }]), want: '文件里的第 1 首歌缺少音源或 ID，没有导入。' },
    { name: '曲目不是对象', content: file([null]), want: '文件里的第 1 首歌缺少音源或 ID，没有导入。' },
  ]
  for (const { name, content, want } of cases) {
    it(name, () => {
      expect(parsePlaylistFile(content)).toBe(want)
    })
  }

  it('元数据类型不对时当作没有', () => {
    const parsed = parsePlaylistFile(file([{ source: 'qq', id: '0039MnYb0qxYhV', title: 7, artist: null, album: ['叶惠美'], durationMs: '269000' }]))
    expect(parsed).toEqual({ name: '通勤', truncated: false, tracks: [{ track: { source: 'qq', id: '0039MnYb0qxYhV' }, title: '', durationMs: 0 }] })
  })

  it('时长不是 0 到 24 小时之间的整数毫秒时当作未知', () => {
    const durations = [-1, 1.5, 86_400_001, 86_400_000]
    const parsed = parsePlaylistFile(file(durations.map((durationMs, index) => ({ source: 'netease', id: String(index), title: '晴天', durationMs }))))
    expect(typeof parsed === 'string' ? parsed : parsed.tracks.map(track => track.durationMs)).toEqual([0, 0, 0, 86_400_000])
  })

  it('歌单名缺少或不是字符串时为空，由服务端换成默认的名字', () => {
    const parsed = parsePlaylistFile(file([sunny.track], { name: 42 }))
    expect(typeof parsed === 'string' ? parsed : parsed.name).toBe('')
  })

  it('超过 1000 首只取前 1000 首，后面的不检查', () => {
    const tracks: unknown[] = Array.from({ length: importLimit }, (_, index) => ({ source: 'netease', id: String(index), title: `第 ${index + 1} 首` }))
    tracks.push({ source: 'spotify', id: 'x' })
    const parsed = parsePlaylistFile(file(tracks))
    if (typeof parsed === 'string')
      throw new Error(parsed)
    expect(parsed.truncated).toBe(true)
    expect(parsed.tracks).toHaveLength(importLimit)
    expect(parsed.tracks.at(-1)?.track).toEqual({ source: 'netease', id: String(importLimit - 1) })
  })

  it('正好 1000 首不算截断', () => {
    const tracks = Array.from({ length: importLimit }, (_, index) => ({ source: 'netease', id: String(index), title: '晴天' }))
    const parsed = parsePlaylistFile(file(tracks))
    expect(typeof parsed === 'string' ? parsed : parsed.truncated).toBe(false)
  })
})
