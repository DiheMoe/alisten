import type { IdentityEvent } from '@/core/api'
import type { MyListen } from '@/core/realtime'
import { api, identityEvents } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { playKey, roomStore, sameTrack } from '@/core/room'
import { notificationStore } from '@/shared/lib/notification-store'
import { Store } from '@/shared/lib/store'

interface HistoryState {
  // 播放历史保存在服务端，还没有读到时为 null。
  listens: MyListen[] | null
  // 最近一次读取失败的原因，读取成功后为 null。
  error: string | null
}

function localDate(date: Date): string {
  return [date.getFullYear(), date.getMonth() + 1, date.getDate()].map(n => String(n).padStart(2, '0')).join('-')
}

function playingKey(): string {
  return playKey(roomStore.state.room?.nowPlaying)
}

class HistoryStore extends Store<HistoryState> {
  #playing = ''
  #pages = 0
  // 只有最后发起的读取能写入。之后又读过一次，或者删除、清空、换了身份之后，此前的读取作废。
  #generation = 0
  #reading = 0

  constructor() {
    super({ listens: null, error: null })
  }

  start(): void {
    this.#playing = playingKey()
    roomStore.addEventListener('change', this.#onRoom)
    identityEvents.on(this.#onIdentity)
  }

  // 每打开一个历史页读取一次。服务端在开播时写入历史，所以页面开着时，正在播放的歌一变就重新读取。返回的函数在页面关闭时调用。
  open(): () => void {
    this.#pages++
    void this.load()
    let open = true
    return () => {
      if (open)
        this.#pages--
      open = false
    }
  }

  // 失败时只设置 error，不抛出。
  async load(): Promise<void> {
    const generation = ++this.#generation
    this.#reading++
    try {
      const { items } = await api.listMyListens()
      if (generation === this.#generation)
        this.setState({ listens: items, error: null })
    }
    catch (error) {
      if (generation === this.#generation)
        this.setState({ error: errorMessage(error) })
    }
    finally {
      this.#reading--
    }
  }

  // 返回 false 时，失败原因已经通过通知显示。
  async remove(listen: MyListen): Promise<boolean> {
    if (!await this.#write(() => api.removeMyListen({ track: listen.track })))
      return false
    this.#changed((this.state.listens ?? []).filter(l => !sameTrack(l.track, listen.track)))
    return true
  }

  // 返回 false 时，失败原因已经通过通知显示。
  async clear(): Promise<boolean> {
    if (!await this.#write(() => api.clearMyListens()))
      return false
    this.#changed([])
    return true
  }

  exportListens(): void {
    const now = new Date()
    const file = new Blob([JSON.stringify({ exportedAt: now.toISOString(), history: this.state.listens ?? [] }, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(file)
    const link = document.createElement('a')
    link.href = url
    link.download = `alisten-history-${localDate(now)}.json`
    link.click()
    // 下载在 click() 时已经开始，到下一个任务再释放对象 URL。
    setTimeout(() => URL.revokeObjectURL(url))
  }

  async #write(request: () => Promise<void>): Promise<boolean> {
    try {
      await request()
      return true
    }
    catch (error) {
      notificationStore.error(errorMessage(error))
      return false
    }
  }

  // 改动之前发起的读取可能带着改动前的历史返回，所以要作废这些读取。有读取被作废时再读一次，免得错过其间开播的歌。
  #changed(listens: MyListen[]): void {
    this.#generation++
    this.setState({ listens })
    if (this.#reading > 0)
      void this.load()
  }

  #onRoom = (): void => {
    const playing = playingKey()
    if (playing === this.#playing)
      return
    this.#playing = playing
    if (playing && this.#pages > 0)
      void this.load()
  }

  // 成员 token 被拒绝时页面不会重新加载。旧身份的历史不能留在页面上，新的游客又还没有播放历史，所以直接清空。
  // 新身份也可能是其他标签页换来的游客，这样的游客可能已经有播放历史，所以换好之后，页面开着就重新读取。
  #onIdentity = (event: IdentityEvent): void => {
    if (event.kind === 'rejected') {
      this.#generation++
      this.setState({ listens: [], error: null })
    }
    else if (event.kind === 'renewed' && this.#pages > 0) {
      void this.load()
    }
  }
}

export const historyStore = new HistoryStore()
