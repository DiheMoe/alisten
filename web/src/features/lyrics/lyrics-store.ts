import type { LyricLine } from './lrc'
import type { NowPlaying } from '@/core/realtime'
import { playKey, roomStore } from '@/core/room'
import { playerStore } from '@/features/playback'
import { lyricsDisplayStore } from '@/features/settings'
import { Store } from '@/shared/lib/store'
import { buildLines, lineIndexAt, lyricLeadMs } from './lrc'

class LyricsStore extends Store<{ lines: LyricLine[], index: number }> {
  #track = ''
  #nowPlaying: NowPlaying | null = null

  constructor() {
    super({ lines: [], index: -1 })
  }

  start(): void {
    roomStore.addEventListener('change', this.#onRoom)
    lyricsDisplayStore.addEventListener('change', this.#onDisplay)
    playerStore.frames.on(positionMs => this.#sync(positionMs))
  }

  #onRoom = (): void => {
    const nowPlaying = roomStore.state.room?.nowPlaying ?? null
    const track = playKey(nowPlaying)
    if (track === this.#track)
      return
    this.#track = track
    this.#nowPlaying = nowPlaying
    const lines = this.#build()
    // 换歌时新歌还没开始播放，播放器给出的仍是上一首的位置。所以先按歌曲开头计算，开始播放后再由每帧的位置更新。
    this.setState({ lines, index: lineIndexAt(lines, 0) })
  }

  // 显示设置只决定带不带翻译和逐字数据，歌词的行数和时间不变，所以当前行不用重新计算。
  #onDisplay = (): void => {
    this.setState({ lines: this.#build() })
  }

  #build(): LyricLine[] {
    const nowPlaying = this.#nowPlaying
    if (!nowPlaying)
      return []
    const { translation, words } = lyricsDisplayStore.state
    const lines = buildLines(nowPlaying.lyric, nowPlaying.lyricWords, translation ? nowPlaying.lyricTrans : '')
    // 关掉逐字时仍用逐字歌词的行和时间，只去掉逐字数据，因为有的歌只有逐字歌词，没有行级歌词。
    return words ? lines : lines.map(line => ({ ...line, words: undefined }))
  }

  #sync(positionMs: number): void {
    this.setState({ index: lineIndexAt(this.state.lines, positionMs + lyricLeadMs) })
  }
}

export const lyricsStore = new LyricsStore()
