import type { Track } from '@/core/realtime'
import { bookTitle } from '@/shared/lib/book-title'

// 点歌按钮的文案和可用状态。临时房间只能点哔哩哔哩的链接，其他链接会被服务端拒绝，所以先禁用并说明原因。
export function pickButton(track: Track, title: string, room: { connected: boolean, ephemeral: boolean }): { label: string, disabled: boolean } {
  if (track.source === 'link' && room.ephemeral)
    return { label: '临时房间不能点链接里的歌', disabled: true }
  return { label: `点歌${bookTitle(title)}`, disabled: !room.connected }
}
