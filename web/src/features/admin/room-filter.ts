import type { RoomListing } from '@/core/realtime'

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
