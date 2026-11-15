import type { PlaylistSort } from './playlist-sort'
import type { MyPlaylist } from '@/core/realtime'
import { sortPlaylists } from './playlist-sort'

export const byUpdate: PlaylistSort = { key: 'updatedAt', order: 'desc' }

// 保持打开对话框时的先后。保存会更新歌单的更新时间，如果按实时的更新时间排，刚勾选的歌单会跳走。打开之后才出现的歌单排在最后。
export function arrange(playlists: readonly MyPlaylist[], order: readonly string[]): MyPlaylist[] {
  const rank = new Map(order.map((id, index) => [id, index]))
  const at = (playlist: MyPlaylist) => rank.get(playlist.id) ?? order.length
  return sortPlaylists(playlists, byUpdate).sort((a, b) => at(a) - at(b))
}

// added 是每个目标歌单新加入的首数，保存失败的是 null。返回保存成功的歌单，和它们里面本来就有的首数之和。
export function saveOutcome(targets: readonly MyPlaylist[], added: readonly (number | null)[], trackCount: number): { saved: MyPlaylist[], skipped: number } {
  const saved = targets.filter((_, index) => added[index] !== null)
  const skipped = added.reduce<number>((sum, count) => count === null ? sum : sum + trackCount - count, 0)
  return { saved, skipped }
}

export function savedMessage(saved: readonly MyPlaylist[], skipped: number): string {
  const where = saved.length === 1 ? `“${saved[0].name}”` : ` ${saved.length} 个歌单`
  return `已保存到${where}${skipped > 0 ? `，其中 ${skipped} 首已经在里面` : ''}`
}
