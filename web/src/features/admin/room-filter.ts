import type { RoomListing } from '@/core/realtime'
import { rowAfterRemoval } from '@/shared/lib/removal-focus'

const roomsPerPage = 20

// 常驻房间的数量有限，一次取回全部，在前端搜索和分页。
export function filterRooms(rooms: RoomListing[], q: string, page: number): { items: RoomListing[], totalPages: number, page: number } {
  const needle = q.trim().toLowerCase()
  const matched = needle
    ? rooms.filter(room => room.name.toLowerCase().includes(needle) || room.slug?.toLowerCase().includes(needle))
    : rooms
  const totalPages = Math.max(1, Math.ceil(matched.length / roomsPerPage))
  const current = Math.min(page, totalPages)
  return { items: matched.slice((current - 1) * roomsPerPage, current * roomsPerPage), totalPages, page: current }
}

// 删掉 removed 之后焦点要落到的那一行，rooms 是删除之前的全部房间。返回 null 时落到标题。
export function roomAfterRemoval(rooms: RoomListing[], removed: string, q: string, page: number): string | null {
  const shown = filterRooms(rooms, q, page)
  const next = filterRooms(rooms.filter(room => room.id !== removed), q, page)
  // 删空了这一页时显示的是上一页，那里没有“下一行”。
  return rowAfterRemoval(shown.items.map(room => room.id), next.page === shown.page ? next.items.map(room => room.id) : [], [removed])
}
