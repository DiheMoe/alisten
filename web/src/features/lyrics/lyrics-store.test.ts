import type { NowPlaying } from '@/core/realtime'
import { describe, expect, it, vi } from 'vitest'
import { Emitter } from '@/shared/lib/emitter'

function nowPlaying(id: string, overrides: Partial<NowPlaying> = {}): NowPlaying {
  return { entry: { id }, mediaUrl: '', startedAtMs: 1000, durationMs: 60_000, gain: 1, lyric: '[00:01.00]行级歌词', ...overrides } as NowPlaying
}

async function setup() {
  vi.resetModules()
  const room = Object.assign(new EventTarget(), { state: { room: null as { nowPlaying: NowPlaying | null } | null } })
  const frames = new Emitter<number>()
  const display = Object.assign(new EventTarget(), { state: { translation: true, words: true } })
  vi.doMock('@/core/room', async importOriginal => ({ ...await importOriginal<typeof import('@/core/room')>(), roomStore: room }))
  vi.doMock('@/features/playback', () => ({ playerStore: { frames } }))
  vi.doMock('@/features/settings', () => ({ lyricsDisplayStore: display }))
  const { lyricsStore } = await import('./lyrics-store')
  lyricsStore.start()
  const play = (next: NowPlaying | null) => {
    room.state = { room: next && { nowPlaying: next } }
    room.dispatchEvent(new Event('change'))
  }
  const show = (state: { translation: boolean, words: boolean }) => {
    display.state = state
    display.dispatchEvent(new Event('change'))
  }
  return { lyricsStore, frames, play, show }
}

describe('lyricsStore', () => {
  it('有逐字歌词和翻译时，歌词行带上逐字数据和翻译', async () => {
    const { lyricsStore, play } = await setup()
    play(nowPlaying('1', { lyricWords: '[1000,500](1000,500,0)逐字', lyricTrans: '[00:01.00]Words' }))
    expect(lyricsStore.state.lines[0]).toEqual({
      timeMs: 1000,
      text: '逐字',
      translation: 'Words',
      words: [{ startMs: 1000, durationMs: 500, text: '逐字' }],
    })
  })

  it('没有逐字歌词时使用行级歌词', async () => {
    const { lyricsStore, play } = await setup()
    play(nowPlaying('1'))
    expect(lyricsStore.state.lines).toEqual([{ timeMs: 1000, text: '行级歌词' }])
  })

  it('换歌时先按歌曲开头计算当前行，之后跟随播放位置', async () => {
    const { lyricsStore, frames, play } = await setup()
    play(nowPlaying('1'))
    frames.emit(30_000)
    expect(lyricsStore.state.index).toBe(0)
    play(nowPlaying('2', { lyric: '[00:01.00]一\n[00:02.00]二' }))
    expect(lyricsStore.state.index).toBe(-1)
    frames.emit(2000)
    expect(lyricsStore.state.index).toBe(1)
  })

  it('比播放位置提前 100ms 切换到下一行', async () => {
    const { lyricsStore, frames, play } = await setup()
    play(nowPlaying('1', { lyric: '[00:01.00]一\n[00:02.00]二' }))
    frames.emit(1899)
    expect(lyricsStore.state.index).toBe(0)
    frames.emit(1900)
    expect(lyricsStore.state.index).toBe(1)
  })

  it('关掉翻译时，歌词行不带翻译', async () => {
    const { lyricsStore, play, show } = await setup()
    show({ translation: false, words: true })
    play(nowPlaying('1', { lyricTrans: '[00:01.00]Line' }))
    expect(lyricsStore.state.lines).toEqual([{ timeMs: 1000, text: '行级歌词' }])
  })

  it('关掉逐字时仍用逐字歌词的行和时间，只去掉逐字数据', async () => {
    const { lyricsStore, play, show } = await setup()
    play(nowPlaying('1', { lyric: '', lyricWords: '[1000,500](1000,500,0)一\n[2000,500](2000,500,0)二' }))
    const withWords = lyricsStore.state.lines
    expect(withWords.some(line => line.words)).toBe(true)
    show({ translation: true, words: false })
    expect(lyricsStore.state.lines).toEqual(withWords.map(({ words: _, ...line }) => line))
  })

  it('播放中切换显示设置时立即重建歌词行，当前行不变', async () => {
    const { lyricsStore, frames, play, show } = await setup()
    play(nowPlaying('1', { lyricWords: '[1000,500](1000,500,0)一\n[2000,500](2000,500,0)二', lyricTrans: '[00:01.00]One\n[00:02.00]Two' }))
    frames.emit(2000)
    const { lines, index } = lyricsStore.state
    show({ translation: false, words: false })
    expect(lyricsStore.state).toEqual({ lines: lines.map(({ words: _, translation: __, ...line }) => line), index })
    show({ translation: true, words: true })
    expect(lyricsStore.state).toEqual({ lines, index })
  })

  it('同一首歌的新快照不重建歌词行，离开房间后清空', async () => {
    const { lyricsStore, play } = await setup()
    play(nowPlaying('1'))
    const { lines } = lyricsStore.state
    play(nowPlaying('1'))
    expect(lyricsStore.state.lines).toBe(lines)
    play(null)
    expect(lyricsStore.state).toEqual({ lines: [], index: -1 })
  })
})
