import type { MyPlaylist } from '@/core/realtime'

export type SortKey = 'name' | 'createdAt' | 'updatedAt' | 'trackCount'
export type SortOrder = 'asc' | 'desc'

export interface PlaylistSort {
  key: SortKey
  order: SortOrder
}

export const sortKeys: SortKey[] = ['name', 'createdAt', 'updatedAt', 'trackCount']
export const sortOrders: SortOrder[] = ['asc', 'desc']

// 换一个字段排序时的方向：名称从 A 到 Z，时间最新在前，歌曲数最多在前。
export function defaultOrder(key: SortKey): SortOrder {
  return key === 'name' ? 'asc' : 'desc'
}

const collator = new Intl.Collator('zh-Hans-CN', { numeric: true, sensitivity: 'base' })

function compareBy(key: SortKey, a: MyPlaylist, b: MyPlaylist): number {
  switch (key) {
    case 'name':
      return collator.compare(a.name, b.name)
    case 'createdAt':
      return a.createdAtMs - b.createdAtMs
    case 'updatedAt':
      return a.updatedAtMs - b.updatedAtMs
    case 'trackCount':
      return a.trackCount - b.trackCount
  }
}

function compareIds(a: MyPlaylist, b: MyPlaylist): number {
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

// 方向作用于整个排序键，所以反向得到的正好是倒过来的列表。“我喜欢”不参与排序，始终在最前。
export function sortPlaylists(list: readonly MyPlaylist[], { key, order }: PlaylistSort): MyPlaylist[] {
  const sign = order === 'asc' ? 1 : -1
  return [...list].sort((a, b) => Number(b.favorites) - Number(a.favorites)
    || sign * (compareBy(key, a, b) || a.createdAtMs - b.createdAtMs || compareIds(a, b)))
}
