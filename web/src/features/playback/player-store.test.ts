import type { NowPlaying } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const startedAtMs = 100_000
const durationMs = 60_000

class FakeAudio extends EventTarget {
  src = ''
  loads = 0
  plays = 0
  currentTime = 0
  paused = true
  volume = 1
  muted = false
  playbackRate = 2
  readyState = 4
  seeking = false
  playError: Error | null = null
  loadError: Error | null = null

  load(): void {
    const error = this.loadError
    this.loadError = null
    if (error)
      throw error
    this.loads += 1
  }

  play(): Promise<void> {
    this.plays += 1
    if (this.playError)
      return Promise.reject(this.playError)
    this.paused = false
    return Promise.resolve()
  }

  pause(): void {
    this.paused = true
  }

  removeAttribute(name: string): void {
    if (name === 'src')
      this.src = ''
  }

  emit(type: string): void {
    this.dispatchEvent(new Event(type))
  }
}

function fakeClock() {
  return Object.assign(new EventTarget(), {
    offsetMs: 0 as number | null,
    nowMs: startedAtMs,
    serverNow() {
      return this.offsetMs === null ? null : this.nowMs
    },
  })
}

function fakeRoom() {
  return Object.assign(new EventTarget(), {
    state: { room: null as { nowPlaying: NowPlaying | null } | null },
    sync: vi.fn(() => Promise.resolve()),
  })
}

function song(id: string, patch: Partial<NowPlaying> = {}): NowPlaying {
  return {
    entry: { id, meta: { title: id, artist: '', album: '' } },
    startedAtMs,
    durationMs,
    mediaUrl: `/media/netease/${id}`,
    gain: 1,
    ...patch,
  } as unknown as NowPlaying
}

let audio: FakeAudio
let clock: ReturnType<typeof fakeClock>
let room: ReturnType<typeof fakeRoom>
let frames: FrameRequestCallback[]

function setPlaying(nowPlaying: NowPlaying | null): void {
  room.state = { room: { nowPlaying } }
  room.dispatchEvent(new Event('change'))
}

function setClock(offsetMs: number | null, nowMs = clock.nowMs): void {
  clock.offsetMs = offsetMs
  clock.nowMs = nowMs
  clock.dispatchEvent(new Event('change'))
}

function nextFrame(): void {
  const frame = frames.shift()
  if (!frame)
    throw new Error('没有等待中的动画帧')
  frame(0)
}

async function attached() {
  const { playerStore } = await import('./player-store')
  playerStore.attach(audio as unknown as HTMLAudioElement, () => {})
  return playerStore
}

const flush = () => vi.advanceTimersByTimeAsync(0)

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
  audio = new FakeAudio()
  clock = fakeClock()
  room = fakeRoom()
  frames = []
  vi.doMock('@/core/clock', () => ({ clock }))
  vi.doMock('@/core/room', () => ({ roomStore: room }))
  vi.stubGlobal('document', { title: '' })
  vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {} })
  vi.stubGlobal('requestAnimationFrame', (frame: FrameRequestCallback) => frames.push(frame))
  vi.stubGlobal('cancelAnimationFrame', () => {
    frames = []
  })
})

afterEach(async () => {
  const { playerStore } = await import('./player-store')
  playerStore.detach()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('开始播放', () => {
  it('加载好之后从应在的位置开始', async () => {
    await attached()
    clock.nowMs = startedAtMs + 5000
    setPlaying(song('a'))
    expect(audio.src).toBe('/media/netease/a')
    expect(audio.loads).toBe(1)
    expect(audio.plays).toBe(0)

    audio.emit('canplay')
    expect(audio.currentTime).toBe(5)
    expect(audio.playbackRate).toBe(1)
    expect(audio.plays).toBe(1)
  })

  it('定位之后再次触发 canplay 不重新开始', async () => {
    await attached()
    setPlaying(song('a'))
    audio.emit('canplay')
    audio.emit('canplay')
    expect(audio.plays).toBe(1)
  })

  it('时钟校准之前等待，校准之后开始', async () => {
    clock.offsetMs = null
    await attached()
    setPlaying(song('a'))
    audio.emit('canplay')
    expect(audio.plays).toBe(0)

    setClock(0, startedAtMs + 2000)
    expect(audio.currentTime).toBe(2)
    expect(audio.plays).toBe(1)

    setClock(5)
    expect(audio.plays).toBe(1)
  })

  it('时钟之后的变化不重新开始', async () => {
    await attached()
    setPlaying(song('a'))
    audio.emit('canplay')
    setClock(10, startedAtMs + 2000)
    expect(audio.plays).toBe(1)
  })

  it('还没到开始时间时停在开头，到时间再播放', async () => {
    await attached()
    clock.nowMs = startedAtMs - 3000
    setPlaying(song('a'))
    audio.currentTime = 9
    audio.emit('canplay')
    expect(audio.currentTime).toBe(0)
    await vi.advanceTimersByTimeAsync(2999)
    expect(audio.plays).toBe(0)

    clock.nowMs = startedAtMs
    await vi.advanceTimersByTimeAsync(1)
    expect(audio.plays).toBe(1)
  })

  it('这首歌已经播完时不再播放', async () => {
    await attached()
    clock.nowMs = startedAtMs + durationMs
    setPlaying(song('a'))
    audio.paused = false
    audio.emit('canplay')
    expect(audio.plays).toBe(0)
    expect(audio.paused).toBe(true)
  })
})

describe('房间状态变化', () => {
  it('同一首歌的新快照不重新加载，但应用新的增益', async () => {
    await attached()
    const current = song('a')
    setPlaying(current)
    setPlaying(song('a', { gain: 0.5 }))
    expect(audio.loads).toBe(1)
    expect(audio.volume).toBeCloseTo(0.375)
  })

  it('同一个条目重新开播时重新加载', async () => {
    await attached()
    setPlaying(song('a'))
    setPlaying(song('a', { startedAtMs: startedAtMs + 1000 }))
    expect(audio.loads).toBe(2)
  })

  it('换歌时取消上一首的等待：新歌要等自己加载好', async () => {
    await attached()
    clock.nowMs = startedAtMs - 3000
    setPlaying(song('a'))
    audio.emit('canplay')
    setPlaying(song('b', { startedAtMs: startedAtMs - 10_000 }))
    await vi.advanceTimersByTimeAsync(3000)
    expect(audio.plays).toBe(0)
    expect(audio.src).toBe('/media/netease/b')
  })

  it('上一首还没加载好就换歌时，只为新歌开始一次', async () => {
    await attached()
    setPlaying(song('a'))
    setPlaying(song('b'))
    audio.emit('canplay')
    expect(audio.plays).toBe(1)
  })

  it('没有在播的歌时停止，清除音频地址', async () => {
    await attached()
    setPlaying(song('a'))
    audio.paused = false
    setPlaying(null)
    expect(audio.paused).toBe(true)
    expect(audio.src).toBe('')
    expect(audio.loads).toBe(2)
  })

  it('已经在等待的开始在停止时取消', async () => {
    await attached()
    clock.nowMs = startedAtMs - 1000
    setPlaying(song('a'))
    audio.emit('canplay')
    expect(vi.getTimerCount()).toBe(1)
    setPlaying(null)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('增益放大有上限，音量不超过 1', async () => {
    await attached()
    setPlaying(song('a', { gain: 1.2 }))
    expect(audio.volume).toBeCloseTo(0.9)
    setPlaying(song('b', { gain: 5 }))
    expect(audio.volume).toBe(1)
    setPlaying(song('c', { gain: -1 }))
    expect(audio.volume).toBe(0)
  })

  it('调音量时立刻生效，增益上限不随音量变化', async () => {
    await attached()
    setPlaying(song('a'))
    const { volumeStore } = await import('./volume-store')
    volumeStore.setVolume(40)
    expect(audio.volume).toBeCloseTo(0.4)
    setPlaying(song('b', { gain: 5 }))
    expect(audio.volume).toBeCloseTo(0.6)
  })
})

describe('自动播放被拒绝', () => {
  it('浏览器要求用户操作时显示提示，点击后恢复', async () => {
    const playerStore = await attached()
    audio.playError = new DOMException('blocked', 'NotAllowedError')
    setPlaying(song('a'))
    audio.emit('canplay')
    await flush()
    expect(playerStore.state.needsGesture).toBe(true)

    audio.playError = null
    playerStore.resume()
    expect(playerStore.state.needsGesture).toBe(false)
    expect(audio.plays).toBe(2)
  })

  it('其他播放错误不要求用户操作', async () => {
    const playerStore = await attached()
    audio.playError = new DOMException('aborted', 'AbortError')
    setPlaying(song('a'))
    audio.emit('canplay')
    await flush()
    expect(playerStore.state.needsGesture).toBe(false)
  })

  it('开始播放之后关掉提示', async () => {
    const playerStore = await attached()
    audio.playError = new DOMException('blocked', 'NotAllowedError')
    setPlaying(song('a'))
    audio.emit('canplay')
    await flush()
    audio.emit('playing')
    expect(playerStore.state.needsGesture).toBe(false)
  })

  it('稍后播放只关掉提示', async () => {
    const playerStore = await attached()
    audio.playError = new DOMException('blocked', 'NotAllowedError')
    setPlaying(song('a'))
    audio.emit('canplay')
    await flush()
    playerStore.dismissGesture()
    expect(playerStore.state.needsGesture).toBe(false)
    expect(audio.plays).toBe(1)
  })
})

describe('恢复播放', () => {
  it('取得最新快照之后按应在的位置再定位一次', async () => {
    const playerStore = await attached()
    setPlaying(song('a'))
    clock.nowMs = startedAtMs + 1000
    playerStore.resume()
    expect(audio.currentTime).toBe(1)
    expect(room.sync).toHaveBeenCalledTimes(1)

    clock.nowMs = startedAtMs + 4000
    await flush()
    expect(audio.currentTime).toBe(4)
  })

  it('同步期间换了歌时不按上一首定位', async () => {
    const playerStore = await attached()
    setPlaying(song('a'))
    let synced = (): void => {}
    room.sync.mockReturnValue(new Promise((resolve) => {
      synced = resolve
    }))
    playerStore.resume()
    const plays = audio.plays
    setPlaying(song('b'))
    synced()
    await flush()
    expect(audio.plays).toBe(plays)
  })

  it('没有在播的歌时什么也不做', async () => {
    const playerStore = await attached()
    playerStore.resume()
    expect(room.sync).not.toHaveBeenCalled()
    expect(audio.plays).toBe(0)
  })
})

describe('播放过程中', () => {
  async function playing() {
    const playerStore = await attached()
    setPlaying(song('a'))
    audio.emit('canplay')
    audio.emit('playing')
    return playerStore
  }

  it('每 2 秒校正超过 1 秒的漂移', async () => {
    await playing()
    audio.currentTime = 10
    clock.nowMs = startedAtMs + 12_000
    await vi.advanceTimersByTimeAsync(2000)
    expect(audio.currentTime).toBe(12)
  })

  it('漂移不超过 1 秒时不打断播放', async () => {
    await playing()
    audio.currentTime = 10
    clock.nowMs = startedAtMs + 10_900
    await vi.advanceTimersByTimeAsync(2000)
    expect(audio.currentTime).toBe(10)
  })

  it('暂停之后不再校正', async () => {
    await playing()
    audio.emit('pause')
    audio.currentTime = 10
    clock.nowMs = startedAtMs + 30_000
    await vi.advanceTimersByTimeAsync(2000)
    expect(audio.currentTime).toBe(10)
  })

  it('已经过了结束时间时不校正', async () => {
    await playing()
    audio.currentTime = 10
    clock.nowMs = startedAtMs + durationMs
    await vi.advanceTimersByTimeAsync(2000)
    expect(audio.currentTime).toBe(10)
  })

  it('每帧发出当前位置，放到时长时暂停，位置不超过时长', async () => {
    const playerStore = await playing()
    const positions: number[] = []
    playerStore.frames.on(position => positions.push(position))
    audio.currentTime = 5
    nextFrame()
    audio.currentTime = 61
    nextFrame()
    expect(positions).toEqual([5000, durationMs])
    expect(audio.paused).toBe(true)
    expect(playerStore.positionMs()).toBe(durationMs)
  })

  it('暂停之后不再发出位置', async () => {
    const playerStore = await playing()
    const positions: number[] = []
    playerStore.frames.on(position => positions.push(position))
    audio.paused = true
    nextFrame()
    expect(positions).toEqual([])
  })
})

describe('卸载', () => {
  it('卸载之后房间和时钟的变化不再影响音频', async () => {
    const playerStore = await attached()
    setPlaying(song('a'))
    audio.emit('canplay')
    audio.emit('playing')
    audio.paused = false
    playerStore.detach()
    expect(audio.paused).toBe(true)
    expect(audio.src).toBe('')
    expect(vi.getTimerCount()).toBe(0)
    expect(frames).toEqual([])

    const { loads, plays } = audio
    setPlaying(song('b'))
    setClock(0)
    audio.emit('canplay')
    expect(audio.loads).toBe(loads)
    expect(audio.plays).toBe(plays)
  })

  it('再次挂载时重新加载正在播放的歌', async () => {
    const playerStore = await attached()
    setPlaying(song('a'))
    playerStore.detach()

    const next = new FakeAudio()
    playerStore.attach(next as unknown as HTMLAudioElement, () => {})
    expect(next.src).toBe('/media/netease/a')
  })

  it('卸载期间换的歌，再次挂载时照常加载', async () => {
    const playerStore = await attached()
    setPlaying(song('a'))
    playerStore.detach()
    setPlaying(song('b'))

    const next = new FakeAudio()
    playerStore.attach(next as unknown as HTMLAudioElement, () => {})
    expect(next.src).toBe('/media/netease/b')
  })

  it('挂载中途失败时清理干净，已移除的音频不再被房间驱动', async () => {
    const { playerStore } = await import('./player-store')
    setPlaying(song('a'))
    audio.loadError = new Error('加载失败')
    expect(() => playerStore.attach(audio as unknown as HTMLAudioElement, () => {})).toThrow('加载失败')

    const { loads } = audio
    setPlaying(song('b'))
    expect(audio.loads).toBe(loads)
  })
})
