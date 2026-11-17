import type { NowPlaying } from '@/core/realtime'
import { brandStore } from '@/core/brand'
import { clock } from '@/core/clock'
import { reconnectDelay } from '@/core/realtime'
import { playKey, roomStore } from '@/core/room'
import { Emitter } from '@/shared/lib/emitter'
import { notificationStore } from '@/shared/lib/notification-store'
import { Store } from '@/shared/lib/store'
import { documentTitle } from './document-title'
import { setMediaSessionHandlers, showMediaSession } from './media-session'
import { driftSeekPosition, haveFutureData, preloadPlan, startPlan } from './timeline'
import { volumeStore } from './volume-store'

const driftCheckIntervalMs = 2000
const maxGain = 1.5
// 音源解析持续失败时，房间里的每个客户端都在请求上游，所以重试要有上限。
const maxMediaRetries = 5
const mediaNoticeMs = 8000

class PlayerStore extends Store<{ needsGesture: boolean }> {
  // 播放期间每帧发出一次当前位置（毫秒）。
  readonly frames = new Emitter<number>()
  // 两个音频元素轮流使用。#active 放当前这首，#standby 提前加载下一首，切到预加载的那首时两者互换。
  #active: HTMLAudioElement | null = null
  #standby: HTMLAudioElement | null = null
  #preloaded: { entryId: string, url: string } | null = null
  // 预加载出错的条目和地址。再遇到同一个就不加载，否则每次房间或时钟变化都会重新请求一次。
  #failedPreload: string | null = null
  // 正在放的元素放这首歌时有没有发出过 playing。在这之前不开始预加载，免得和这首歌的缓冲抢带宽。
  #playing = false
  #current: NowPlaying | null = null
  #load = 0
  #failures = 0
  #awaitingClock = false
  #startTimer: ReturnType<typeof setTimeout> | undefined
  #retryTimer: ReturnType<typeof setTimeout> | undefined
  #preloadTimer: ReturnType<typeof setTimeout> | undefined
  #driftTimer: ReturnType<typeof setInterval> | undefined
  #frame: number | undefined

  constructor() {
    super({ needsGesture: false })
  }

  start(): void {
    brandStore.addEventListener('change', this.#showTitle)
    this.#showTitle()
  }

  attach(active: HTMLAudioElement, standby: HTMLAudioElement, onNextTrack: () => void): void {
    this.#active = active
    this.#standby = standby
    try {
      for (const audio of [active, standby]) {
        audio.addEventListener('playing', this.#onPlaying)
        audio.addEventListener('pause', this.#onPause)
        audio.addEventListener('error', this.#onError)
      }
      standby.muted = true
      document.addEventListener('click', this.#unlockStandby, { capture: true })
      roomStore.addEventListener('change', this.#onRoom)
      clock.addEventListener('change', this.#onClock)
      volumeStore.addEventListener('change', this.#applyVolume)
      setMediaSessionHandlers({ play: () => this.resume(), pause: () => this.#active?.pause(), nexttrack: onNextTrack })
      this.#onRoom()
    }
    catch (error) {
      // 挂载中途失败时必须清理，否则房间的订阅仍会在已移除的 <audio> 上加载和播放，重新挂载的 <audio> 也不会再加载这首歌。
      this.detach()
      throw error
    }
  }

  detach(): void {
    const active = this.#active
    const standby = this.#standby
    if (!active || !standby)
      return
    this.#stop()
    // pause 事件要到下一个任务才派发，那时监听已经移除，所以定时器和 rAF 只能在这里取消。
    this.#stopTicking()
    for (const audio of [active, standby]) {
      audio.removeEventListener('playing', this.#onPlaying)
      audio.removeEventListener('pause', this.#onPause)
      audio.removeEventListener('error', this.#onError)
    }
    document.removeEventListener('click', this.#unlockStandby, { capture: true })
    roomStore.removeEventListener('change', this.#onRoom)
    clock.removeEventListener('change', this.#onClock)
    volumeStore.removeEventListener('change', this.#applyVolume)
    setMediaSessionHandlers(null)
    showMediaSession(null)
    this.#current = null
    this.#failedPreload = null
    this.#active = null
    this.#standby = null
  }

  positionMs(): number {
    const audio = this.#active
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
    // 出错的音频元素调用 play() 一定失败，只能重新加载。
    if (this.#active?.error)
      this.#reload(current)
    else
      this.#start()
    // 恢复播放时重新获取快照。即使快照没有变化，也按应在位置重新定位一次。
    void roomStore.sync().then(() => {
      if (playKey(this.#current) === playKey(current))
        this.#start()
    })
  }

  dismissGesture(): void {
    this.setState({ needsGesture: false })
  }

  #onRoom = (): void => {
    this.#follow(roomStore.state.room?.nowPlaying ?? null)
    this.#planPreload()
  }

  #follow(next: NowPlaying | null): void {
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
    if (playKey(previous) === playKey(next)) {
      // 同一首歌的新快照来自重连的握手或 room.sync，说明连接恢复了，所以不等下一次重试。重试用完之后也靠这样的快照恢复。
      if (this.#active?.error)
        this.#reload(next)
      return
    }
    clearTimeout(this.#startTimer)
    clearTimeout(this.#retryTimer)
    this.#failures = 0
    this.#playing = false
    const load = ++this.#load
    if (this.#swapToPreloaded(next))
      this.#startPreloaded(load)
    else
      this.#loadSource(next.mediaUrl, load)
  }

  #showTitle = (): void => {
    document.title = documentTitle(brandStore.state.name, this.#current?.entry.meta ?? null)
  }

  // 开播的正是备用元素预加载的那首时，两个元素互换并返回 true。原来正在放的元素停下、清空，成为新的备用元素。
  #swapToPreloaded(next: NowPlaying): boolean {
    const active = this.#active
    const standby = this.#standby
    const preloaded = this.#preloaded
    if (!active || !standby || !preloaded || preloaded.entryId !== next.entry.id || preloaded.url !== next.mediaUrl)
      return false
    this.#preloaded = null
    this.#playing = false
    this.#stopTicking()
    active.pause()
    active.removeAttribute('src')
    active.load()
    active.muted = true
    this.#active = standby
    this.#standby = active
    this.#applyVolume()
    return true
  }

  // 互换之后马上开始，不等 canplay。iOS 上备用元素没解锁时等不到 canplay，马上调用 play() 才会得到 NotAllowedError，走“点击播放”的提示。还没缓冲好的，canplay 时再按应在的位置定位一次。
  #startPreloaded(load: number): void {
    const audio = this.#active
    if (!audio)
      return
    this.#start()
    if (audio.readyState < haveFutureData)
      this.#startOnCanPlay(audio, load)
  }

  // 定位之后还会再次触发 canplay，每次加载只处理第一次。
  #startOnCanPlay(audio: HTMLAudioElement, load: number): void {
    audio.addEventListener('canplay', () => {
      if (load === this.#load)
        this.#start()
    }, { once: true })
  }

  #loadSource(url: string, load: number): void {
    const audio = this.#active
    if (!audio)
      return
    this.#startOnCanPlay(audio, load)
    audio.src = url
    audio.load()
  }

  #planPreload = (): void => {
    clearTimeout(this.#preloadTimer)
    const next = roomStore.state.room?.next ?? null
    const preloaded = this.#preloaded
    if (preloaded && (!next || preloaded.entryId !== next.entryId || preloaded.url !== next.mediaUrl))
      this.#dropPreload()
    const plan = preloadPlan(next, this.#current, clock.serverNow())
    if (plan.kind === 'wait')
      this.#preloadTimer = setTimeout(this.#planPreload, plan.delayMs)
    else if (plan.kind === 'load' && this.#playing && !this.#preloaded && this.#failedPreload !== `${plan.entryId} ${plan.url}`)
      this.#preload(plan.entryId, plan.url)
  }

  #preload(entryId: string, url: string): void {
    const standby = this.#standby
    if (!standby)
      return
    this.#preloaded = { entryId, url }
    standby.src = url
    standby.load()
  }

  #dropPreload(): void {
    this.#preloaded = null
    const standby = this.#standby
    if (!standby)
      return
    standby.removeAttribute('src')
    standby.load()
  }

  // 第一次点击时对备用元素调一次 load()，因为 iOS 要求每个媒体元素在用户操作中解锁一次，否则互换之后 play() 会被拒绝。
  // 只认 click，因为 iOS 上滑动结束的 touchend 和修饰键这类按键都不算用户操作，在里面调 load() 解不了锁，解锁的机会却用掉了。
  // 点一下屏幕，或者在按钮上按回车、空格，都会产生 click。
  #unlockStandby = (): void => {
    document.removeEventListener('click', this.#unlockStandby, { capture: true })
    this.#standby?.load()
  }

  #reload(current: NowPlaying): void {
    clearTimeout(this.#retryTimer)
    this.#loadSource(current.mediaUrl, ++this.#load)
  }

  #start(): void {
    clearTimeout(this.#startTimer)
    const audio = this.#active
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
    this.#planPreload()
  }

  #onError = (event: Event): void => {
    if (event.currentTarget === this.#standby) {
      const preloaded = this.#preloaded
      if (preloaded)
        this.#failedPreload = `${preloaded.entryId} ${preloaded.url}`
      this.#dropPreload()
      return
    }
    const current = this.#current
    if (!current)
      return
    this.#failures += 1
    if (this.#failures === 1) {
      // /media 在同一实例内复用 1 分钟内取到的播放地址，所以只有取到 1 分钟以后才失效的地址，这次重试才能换到新地址。
      this.#reload(current)
      return
    }
    if (this.#failures === 2)
      notificationStore.error('这首歌暂时放不了，正在重试。', mediaNoticeMs)
    if (this.#failures <= maxMediaRetries)
      this.#retryTimer = setTimeout(() => this.#reload(current), reconnectDelay(this.#failures - 1))
    else if (this.#failures === maxMediaRetries + 1)
      notificationStore.error('这首歌没能加载，已停止重试。', mediaNoticeMs)
  }

  #onPlaying = (event: Event): void => {
    if (event.currentTarget !== this.#active)
      return
    this.#failures = 0
    this.setState({ needsGesture: false })
    cancelAnimationFrame(this.#frame ?? 0)
    this.#frame = requestAnimationFrame(this.#tick)
    clearInterval(this.#driftTimer)
    this.#driftTimer = setInterval(this.#checkDrift, driftCheckIntervalMs)
    this.#playing = true
    this.#planPreload()
  }

  #onPause = (event: Event): void => {
    if (event.currentTarget === this.#active)
      this.#stopTicking()
  }

  #stopTicking(): void {
    cancelAnimationFrame(this.#frame ?? 0)
    this.#frame = undefined
    clearInterval(this.#driftTimer)
  }

  #tick = (): void => {
    const audio = this.#active
    const current = this.#current
    if (!audio || !current || audio.paused) {
      this.#frame = undefined
      return
    }
    const position = audio.currentTime * 1000
    if (position >= current.durationMs)
      audio.pause()
    // 先预约下一帧再通知，因为 Emitter 不隔离监听的异常。某个监听出错时，进度条和歌词不会因此停在这一帧。
    this.#frame = requestAnimationFrame(this.#tick)
    this.frames.emit(Math.min(position, current.durationMs))
  }

  #checkDrift = (): void => {
    const audio = this.#active
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
    const audio = this.#active
    if (!audio)
      return
    const gain = Math.min(maxGain, Math.max(0, this.#current?.gain ?? 1))
    audio.volume = Math.min(1, volumeStore.state.volume / 100 * gain)
    audio.muted = volumeStore.state.muted
  }

  #stop(): void {
    ++this.#load
    clearTimeout(this.#startTimer)
    clearTimeout(this.#retryTimer)
    clearTimeout(this.#preloadTimer)
    this.#awaitingClock = false
    this.#playing = false
    this.setState({ needsGesture: false })
    this.#dropPreload()
    const audio = this.#active
    if (!audio)
      return
    audio.pause()
    audio.removeAttribute('src')
    audio.load()
  }
}

export const playerStore = new PlayerStore()
