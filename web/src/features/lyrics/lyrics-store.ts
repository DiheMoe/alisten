import type { LyricLine } from './lrc'
import { roomStore } from '@/core/room'
import { playerStore } from '@/features/playback'
import { Store } from '@/shared/lib/store'
import { lineIndexAt, parseLrc } from './lrc'

class LyricsStore extends Store<{ lines: LyricLine[], index: number }> {
  #track = ''

  constructor() {
    super({ lines: [], index: -1 })
  }

  start(): void {
    roomStore.addEventListener('change', this.#onRoom)
    playerStore.frames.on(positionMs => this.#sync(positionMs))
  }

  #onRoom = (): void => {
    const nowPlaying = roomStore.state.room?.nowPlaying ?? null
    const track = nowPlaying ? `${nowPlaying.entry.id}@${nowPlaying.startedAtMs}` : ''
    if (track === this.#track)
      return
    this.#track = track
    const lines = parseLrc(nowPlaying?.lyric ?? '')
    // 换歌时新歌尚未开始播放，播放器给出的仍是上一首的位置：先按歌曲开头计算，开始播放后再由每帧的位置更新。
    this.setState({ lines, index: lineIndexAt(lines, 0) })
  }

  #sync(positionMs: number): void {
    this.setState({ index: lineIndexAt(this.state.lines, positionMs) })
  }
}

export const lyricsStore = new LyricsStore()
