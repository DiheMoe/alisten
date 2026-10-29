import type { Member } from '@/core/realtime'
import type { RoomNotice } from '@/core/room'
import { clock } from '@/core/clock'
import { roomStore } from '@/core/room'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'

type ChatItem
  = | { kind: 'message', key: string, member: Member, text: string, atMs: number }
    | { kind: 'activity', key: string, member: Member | null, text: string, atMs: number }

// 不保留聊天记录：只显示这次进入房间之后收到的消息和房间动态，换房间时清空。
class ChatStore extends Store<{ items: ChatItem[] }> {
  #roomId: string | null = null

  constructor() {
    super({ items: [] })
  }

  start(): void {
    roomStore.addEventListener('change', this.#onRoom)
    roomStore.notices.on(this.#onNotice)
  }

  async send(content: string): Promise<void> {
    await roomStore.requests.chatSend({ content })
  }

  #onRoom = (): void => {
    const { roomId } = roomStore.state
    if (roomId === this.#roomId)
      return
    this.#roomId = roomId
    this.setState({ items: [] })
  }

  #onNotice = (notice: RoomNotice): void => {
    const key = `${notice.term}:${notice.seq}`
    if (notice.type === 'chat.message') {
      const { member, content, sentAtMs } = notice.data
      this.#append({ kind: 'message', key, member, text: content, atMs: sentAtMs })
      return
    }
    const { kind, member, entry, message } = notice.data
    // 房间动态不带时间，按收到的时间显示。
    this.#append({ kind: 'activity', key, member: member ?? null, text: message, atMs: clock.serverNow() ?? Date.now() })
    // 自己点的歌准备失败时，聊天可能没有打开，所以另外用通知提醒。
    if (kind === 'failed' && entry?.by && entry.by.id === roomStore.state.member?.id)
      notificationStore.error(message)
  }

  #append(item: ChatItem): void {
    this.setState({ items: [...this.state.items, item] })
  }
}

export const chatStore = new ChatStore()
