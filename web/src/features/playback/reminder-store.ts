import type { Entry } from '@/core/realtime'
import type { RoomState } from '@/core/room'
import { clock } from '@/core/clock'
import { roomStore } from '@/core/room'
import { bookTitle } from '@/shared/lib/book-title'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'
import { shouldRemind } from './reminder'

const enabledKey = 'alisten.reminder.enabled'
const systemNotificationsKey = 'alisten.reminder.system-notifications'

interface ReminderSettings {
  enabled: boolean
  systemNotifications: boolean
}

export function systemNotificationsSupported(): boolean {
  return 'Notification' in window && 'serviceWorker' in navigator
}

// 只能在用户操作的事件里调用：Safari 和 Firefox 不接受其他时候发起的权限申请。
export async function requestNotificationPermission(): Promise<boolean> {
  return await Notification.requestPermission() === 'granted'
}

class ReminderStore extends Store<ReminderSettings> {
  #room: RoomState | null = null
  #firstSeen = new Map<string, number>()

  constructor() {
    super({ enabled: true, systemNotifications: false })
  }

  start(): void {
    this.setState({
      enabled: localStorage.getItem(enabledKey) !== 'false',
      systemNotifications: localStorage.getItem(systemNotificationsKey) === 'true',
    })
    roomStore.addEventListener('change', this.#onRoom)
  }

  updateSettings({ enabled, systemNotifications }: ReminderSettings): void {
    localStorage.setItem(enabledKey, String(enabled))
    localStorage.setItem(systemNotificationsKey, String(systemNotifications))
    this.setState({ enabled, systemNotifications })
  }

  #onRoom = (): void => {
    const { room, live, member } = roomStore.state
    if (room === this.#room)
      return
    const previous = this.#room?.nowPlaying ?? null
    this.#room = room
    if (!room || !member) {
      this.#firstSeen.clear()
      return
    }
    const now = clock.now()
    const next = room.nowPlaying
    if (next) {
      const firstSeen = this.#firstSeen.get(next.entry.id)
      if (shouldRemind(previous, next, member.id, live, this.state.enabled, firstSeen === undefined ? null : now - firstSeen))
        this.#remind(room.room.id, next.entry)
    }
    const own = new Set([...room.items, room.pending, next?.entry].flatMap(entry => entry?.by?.id === member.id ? [entry.id] : []))
    // 开播时服务端先用 queue.updated 把条目移出队列，再用 playback.updated 开始播放，两条事件之间的状态里没有这个条目，
    // 所以只在快照和正在播放的歌变化时删掉不在房间状态里的记录。
    if (!live || next !== previous) {
      for (const id of this.#firstSeen.keys()) {
        if (!own.has(id))
          this.#firstSeen.delete(id)
      }
    }
    for (const id of own) {
      if (!this.#firstSeen.has(id))
        this.#firstSeen.set(id, now)
    }
  }

  #remind(roomId: string, { id, meta }: Entry): void {
    notificationStore.info(`轮到你点的${bookTitle(meta.title)}了`)
    if (!this.state.systemNotifications || document.visibilityState !== 'hidden' || !systemNotificationsSupported() || Notification.permission !== 'granted')
      return
    // Android 上的 Chrome 不允许页面直接 new Notification()。
    void navigator.serviceWorker.ready.then(registration => registration.showNotification('轮到你点的歌了', {
      body: meta.artist ? `${meta.title} - ${meta.artist}` : meta.title,
      tag: `${roomId}:${id}`,
    }))
  }
}

export const reminderStore = new ReminderStore()
