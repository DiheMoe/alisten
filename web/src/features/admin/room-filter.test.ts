import type { RoomListing } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { filterRooms } from './room-filter'

function room(id: string, name: string, slug: string | null = null): RoomListing {
  return { id, name, slug, description: '', hasPassword: false, kind: 'persistent', onlineCount: 0 }
}

function rooms(count: number): RoomListing[] {
  return Array.from({ length: count }, (_, index) => room(String(index + 1), `房间 ${index + 1}`))
}

const ids = (list: RoomListing[]) => list.map(item => item.id)

describe('filterRooms', () => {
  const sample = [room('a', '大猫听歌房', 'livehouse'), room('b', 'MyTestRoom'), room('c', '深夜电台', 'Night')]

  it('空查询返回全部', () => {
    expect(filterRooms(sample, '', 1)).toEqual({ items: sample, totalPages: 1, page: 1 })
    expect(ids(filterRooms(sample, '  ', 1).items)).toEqual(['a', 'b', 'c'])
  })

  it('按名称或别名匹配，不区分大小写', () => {
    expect(ids(filterRooms(sample, 'mytest', 1).items)).toEqual(['b'])
    expect(ids(filterRooms(sample, 'LIVE', 1).items)).toEqual(['a'])
    expect(ids(filterRooms(sample, 'night', 1).items)).toEqual(['c'])
    expect(ids(filterRooms(sample, '电台', 1).items)).toEqual(['c'])
    expect(filterRooms(sample, 'jazz', 1)).toEqual({ items: [], totalPages: 1, page: 1 })
  })

  it('每页 20 条', () => {
    const all = rooms(45)
    const first = filterRooms(all, '', 1)
    expect(first.totalPages).toBe(3)
    expect(ids(first.items)).toEqual(ids(all.slice(0, 20)))
    expect(ids(filterRooms(all, '', 3).items)).toEqual(['41', '42', '43', '44', '45'])
  })

  it('页码超出时退到最后一页', () => {
    const result = filterRooms(rooms(45), '', 9)
    expect(result.page).toBe(3)
    expect(result.items).toHaveLength(5)
    expect(filterRooms(rooms(45), '房间 4', 2)).toMatchObject({ totalPages: 1, page: 1 })
    expect(filterRooms([], '', 2)).toEqual({ items: [], totalPages: 1, page: 1 })
  })
})
