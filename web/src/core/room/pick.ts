import type { Track } from '@/core/realtime'
import { errorMessage } from '@/core/errors'
import { RequestError } from '@/core/realtime'
import { bookTitle } from '@/shared/lib/book-title'
import { notificationStore } from '@/shared/lib/notification-store'
import { roomStore } from './room-store'

// 点歌按钮的文案和可用状态。临时房间只能点哔哩哔哩的链接，其他链接会被服务端拒绝，所以先禁用并说明原因。
export function pickButton(track: Track, title: string, room: { connected: boolean, ephemeral: boolean }): { label: string, disabled: boolean } {
  if (track.source === 'link' && room.ephemeral)
    return { label: '临时房间不能点链接里的歌', disabled: true }
  return { label: `点歌${bookTitle(title)}`, disabled: !room.connected }
}

// 返回 false 时，失败原因已经通过通知显示。
export async function enqueue(track: Track): Promise<boolean> {
  try {
    const { entry } = await roomStore.requests.queueAdd({ track })
    notificationStore.success(`已点歌${bookTitle(entry.meta.title)}`)
    return true
  }
  catch (error) {
    // 点了认得出却不能点的 B 站链接时，INVALID_ARGUMENT 的 message 就是写给点歌人的原因。
    notificationStore.error(track.source === 'link' && error instanceof RequestError && error.code === 'INVALID_ARGUMENT' ? error.message : errorMessage(error))
    return false
  }
}
