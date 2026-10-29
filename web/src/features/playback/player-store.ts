import type { NowPlaying } from '@/core/realtime'
import { brandStore } from '@/core/brand'
import { clock } from '@/core/clock'
import { roomStore } from '@/core/room'
import { Emitter } from '@/shared/lib/emitter'
import { Store } from '@/shared/lib/store'
import { documentTitle } from './document-title'
import { setMediaSessionHandlers, showMediaSession } from './media-session'
import { driftSeekPosition, startPlan } from './timeline'
import { volumeStore } from './volume-store'

const driftCheckIntervalMs = 2000
const maxGain = 1.5

function sameTrack(a: NowPlaying, b: NowPlaying): boolean {
  return a.entry.id === b.entry.id && a.startedAtMs === b.startedAtMs
}

class PlayerStore extends Store<{ needsGesture: boolean }> {
  // 播放期间每帧发出一次当前位置（毫秒）。
  readonly frames = new Emitter<number>()
  #audio: HTMLAudioElement | null = null
  #current: NowPlaying | null = null
  #load = 0
  #reloaded = false
  #awaitingClock = false
  #startTimer: ReturnType<typeof setTimeout> | undefined
  #driftTimer: ReturnType<typeof setInterval> | undefined
  #frame: number | undefined

  constructor() {
    super({ needsGesture: false })
  }

  start(): void {
    brandStore.addEventListener('change', this.#showTitle)
    this.#showTitle()
  }

  attach(audio: HTMLAudioElement, onNextTrack: () => void): void {
    this.#audio = audio
    try {
      audio.addEventListener('playing', this.#onPlaying)
      audio.addEventListener('pause', this.#onPause)
      audio.addEventListener('error', this.#onError)
      roomStore.addEventListener('change', this.#onRoom)
      clock.addEventListener('change', this.#onClock)
      volumeStore.addEventListener('change', this.#applyVolume)
      setMediaSessionHandlers({ play: () => this.resume(), pause: () => audio.pause(), nexttrack: onNextTrack })
      this.#onRoom()
    }
    catch (error) {
      // 挂载中途失败时必须清理：否则房间的订阅仍会在已移除的 <audio> 上加载和播放，重新挂载的 <audio> 也不会再加载这首歌。
      this.detach()
      throw error
    }
  }

  detach(): void {
    const audio = this.#audio
    if (!audio)
      return
    this.#stop()
    // pause 事件要到下一个任务才派发，那时监听已经移除，定时器和 rAF 只能在这里取消。
    this.#onPause()
    audio.removeEventListener('playing', this.#onPlaying)
    audio.removeEventListener('pause', this.#onPause)
    audio.removeEventListener('error', this.#onError)
    roomStore.removeEventListener('change', this.#onRoom)
    clock.removeEventListener('change', this.#onClock)
    volumeStore.removeEventListener('change', this.#applyVolume)
    setMediaSessionHandlers(null)
    showMediaSession(null)
    this.#current = null
    this.#audio = null
  }

  positionMs(): number {
    const audio = this.#audio
    const current = this.#current
    if (!audio || !current)
      return 0
    return Math.min(audio.currentTime * 1000, current.durationMs)
  }

  resume(): void {
    const current = this.#current
    if (!current)
      return
    this.setState({ needsGesture: false })
    this.#start()
    // 恢复播放时重新获取快照；即使快照没有变化，也按应在位置重新定位一次。
    void roomStore.sync().then(() => {
      if (this.#current && sameTrack(this.#current, current))
        this.#start()
    })
  }

  dismissGesture(): void {
    this.setState({ needsGesture: false })
  }

  #onRoom = (): void => {
    const next = roomStore.state.room?.nowPlaying ?? null
    const previous = this.#current
    if (next === previous)
      return
    this.#current = next
    this.#applyVolume()
    showMediaSession(next)
    this.#showTitle()
    if (!next) {
      this.#stop()
      return
    }
    if (previous && sameTrack(previous, next))
      return
    clearTimeout(this.#startTimer)
    this.#reloaded = false
    this.#loadSource(next.mediaUrl, ++this.#load)
  }

  #showTitle = (): void => {
    document.title = documentTitle(brandStore.state.name, this.#current?.entry.meta ?? null)
  }

  #loadSource(url: string, load: number): void {
    const audio = this.#audio
    if (!audio)
      return
    // 定位之后还会再次触发 canplay，每次加载只处理第一次。
    audio.addEventListener('canplay', () => {
      if (load === this.#load)
        this.#start()
    }, { once: true })
    audio.src = url
    audio.load()
  }

  #start(): void {
    clearTimeout(this.#startTimer)
    const audio = this.#audio
    const current = this.#current
    if (!audio || !current)
      return
    const now = clock.serverNow()
    if (now === null) {
      this.#awaitingClock = true
      return
    }
    this.#awaitingClock = false
    const plan = startPlan(now - current.startedAtMs, current.durationMs)
    switch (plan.kind) {
      case 'wait':
        audio.currentTime = 0
        this.#startTimer = setTimeout(() => this.#start(), plan.delayMs)
        return
      case 'ended':
        audio.pause()
        return
      case 'play':
        audio.currentTime = plan.positionMs / 1000
        audio.playbackRate = 1
        audio.play().catch((error: unknown) => {
          if (error instanceof DOMException && error.name === 'NotAllowedError')
            this.setState({ needsGesture: true })
        })
    }
  }

  #onClock = (): void => {
    if (this.#awaitingClock && clock.offsetMs !== null)
      this.#start()
  }

  #onError = (): void => {
    const current = this.#current
    if (!current || this.#reloaded)
      return
    this.#reloaded = true
    // /media 在同一实例内复用 1 分钟内取到的播放地址，所以只有取到 1 分钟以后才失效的地址，这次重试才能换到新地址。
    this.#loadSource(current.mediaUrl, ++this.#load)
  }

  #onPlaying = (): void => {
    this.setState({ needsGesture: false })
    cancelAnimationFrame(this.#frame ?? 0)
    this.#frame = requestAnimationFrame(this.#tick)
    clearInterval(this.#driftTimer)
    this.#driftTimer = setInterval(this.#checkDrift, driftCheckIntervalMs)
  }

  #onPause = (): void => {
    cancelAnimationFrame(this.#frame ?? 0)
    this.#frame = undefined
    clearInterval(this.#driftTimer)
  }

  #tick = (): void => {
    const audio = this.#audio
    const current = this.#current
    if (!audio || !current || audio.paused) {
      this.#frame = undefined
      return
    }
    const position = audio.currentTime * 1000
    if (position >= current.durationMs)
      audio.pause()
    this.frames.emit(Math.min(position, current.durationMs))
    this.#frame = requestAnimationFrame(this.#tick)
  }

  #checkDrift = (): void => {
    const audio = this.#audio
    const current = this.#current
    const now = clock.serverNow()
    if (!audio || !current || now === null)
      return
    const expected = now - current.startedAtMs
    if (expected >= current.durationMs)
      return
    const positionMs = driftSeekPosition({
      positionMs: audio.currentTime * 1000,
      paused: audio.paused,
      seeking: audio.seeking,
      readyState: audio.readyState,
    }, expected)
    if (positionMs !== null)
      audio.currentTime = positionMs / 1000
  }

  #applyVolume = (): void => {
    const audio = this.#audio
    if (!audio)
      return
    const gain = Math.min(maxGain, Math.max(0, this.#current?.gain ?? 1))
    audio.volume = Math.min(1, volumeStore.state.volume / 100 * gain)
    audio.muted = volumeStore.state.muted
  }

  #stop(): void {
    ++this.#load
    clearTimeout(this.#startTimer)
    this.#awaitingClock = false
    this.setState({ needsGesture: false })
    const audio = this.#audio
    if (!audio)
      return
    audio.pause()
    audio.removeAttribute('src')
    audio.load()
  }
}

export const playerStore = new PlayerStore()
