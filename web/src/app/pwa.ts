import { registerSW } from 'virtual:pwa-register'
import { CloseCode, reconnectDelay } from '@/core/realtime'
import { roomStore } from '@/core/room'
import { Store } from '@/shared/lib/store'

const updateCheckIntervalMs = 30 * 60 * 1000
const outdatedReloadsKey = 'alisten.outdated-reloads'

class PwaStore extends Store<{ needRefresh: boolean }> {
  #registration: ServiceWorkerRegistration | undefined
  #updateServiceWorker: (() => Promise<void>) | undefined
  #outdated = false

  constructor() {
    super({ needRefresh: false })
  }

  start(): void {
    this.#updateServiceWorker = registerSW({
      onRegisteredSW: (_url, registration) => {
        this.#registration = registration
        setInterval(() => {
          if (document.visibilityState === 'visible')
            void registration?.update()
        }, updateCheckIntervalMs)
      },
      onNeedRefresh: () => {
        if (this.#outdated)
          void this.#updateServiceWorker?.()
        else
          this.setState({ needRefresh: true })
      },
    })
    roomStore.addEventListener('change', this.#onSession)
  }

  // 新的 Service Worker 接管页面后，由 vite-plugin-pwa 刷新页面。
  update(): void {
    this.setState({ needRefresh: false })
    void this.#updateServiceWorker?.()
  }

  dismiss(): void {
    this.setState({ needRefresh: false })
  }

  #onSession = (): void => {
    const { status, stopCode } = roomStore.state
    // 握手成功说明前端与服务端的协议版本一致。
    if (status === 'open')
      sessionStorage.removeItem(outdatedReloadsKey)
    if (stopCode === CloseCode.clientOutdated)
      this.#reloadOutdated()
  }

  // 先让新的 Service Worker 接管再刷新，否则刷新后加载的仍是缓存里的旧版本。
  // 发布期间前端和服务端可能短暂处于不同的协议版本，因此连续刷新的间隔按重连的退避规则递增。
  #reloadOutdated(): void {
    if (this.#outdated)
      return
    this.#outdated = true
    const reloads = Number(sessionStorage.getItem(outdatedReloadsKey) ?? 0) + 1
    sessionStorage.setItem(outdatedReloadsKey, String(reloads))
    const registration = this.#registration
    if (registration?.waiting)
      void this.#updateServiceWorker?.()
    else
      void registration?.update()
    setTimeout(() => window.location.reload(), reconnectDelay(reloads))
  }
}

export const pwaStore = new PwaStore()
