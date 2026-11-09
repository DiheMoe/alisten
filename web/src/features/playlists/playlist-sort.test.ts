import type { PlaylistSort } from './playlist-sort'
import type { MyPlaylist } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { sortPlaylists } from './playlist-sort'

function playlist(id: string, name: string, trackCount: number, createdAtMs: number, updatedAtMs: number, favorites = false): MyPlaylist {
  return { id, name, favorites, trackCount, createdAtMs, updatedAtMs, coverUrl: null }
}

const favorites = playlist('p0', '我喜欢', 3, 50, 50, true)
const commute = playlist('p1', '通勤', 36, 100, 400)
const chill = playlist('p2', 'Chill', 5, 200, 300)
const running = playlist('p3', '晨跑', 12, 300, 200)
const night = playlist('p4', '安静的夜晚', 0, 400, 500)
const all = [commute, favorites, chill, running, night]

function names(sort: PlaylistSort, list: MyPlaylist[] = all): string[] {
  return sortPlaylists(list, sort).map(p => p.name)
}

describe('sortPlaylists', () => {
  it('按名称排序，中文按拼音', () => {
    expect(names({ key: 'name', order: 'asc' })).toEqual(['我喜欢', '安静的夜晚', '晨跑', '通勤', 'Chill'])
    expect(names({ key: 'name', order: 'desc' })).toEqual(['我喜欢', 'Chill', '通勤', '晨跑', '安静的夜晚'])
  })

  it('名称里的数字按大小排', () => {
    const list = [playlist('a', '歌单 10', 0, 1, 1), playlist('b', '歌单 2', 0, 2, 2)]
    expect(names({ key: 'name', order: 'asc' }, list)).toEqual(['歌单 2', '歌单 10'])
  })

  it('按创建时间排序', () => {
    expect(names({ key: 'createdAt', order: 'desc' })).toEqual(['我喜欢', '安静的夜晚', '晨跑', 'Chill', '通勤'])
    expect(names({ key: 'createdAt', order: 'asc' })).toEqual(['我喜欢', '通勤', 'Chill', '晨跑', '安静的夜晚'])
  })

  it('按更新时间排序', () => {
    expect(names({ key: 'updatedAt', order: 'desc' })).toEqual(['我喜欢', '安静的夜晚', '通勤', 'Chill', '晨跑'])
    expect(names({ key: 'updatedAt', order: 'asc' })).toEqual(['我喜欢', '晨跑', 'Chill', '通勤', '安静的夜晚'])
  })

  it('按歌曲数排序', () => {
    expect(names({ key: 'trackCount', order: 'desc' })).toEqual(['我喜欢', '通勤', '晨跑', 'Chill', '安静的夜晚'])
    expect(names({ key: 'trackCount', order: 'asc' })).toEqual(['我喜欢', '安静的夜晚', 'Chill', '晨跑', '通勤'])
  })

  it('“我喜欢”的值排在最后时也置顶', () => {
    const empty = { ...favorites, name: '我喜欢', trackCount: 0, updatedAtMs: 0 }
    expect(sortPlaylists([commute, empty], { key: 'trackCount', order: 'desc' })[0]).toBe(empty)
    expect(sortPlaylists([commute, empty], { key: 'updatedAt', order: 'desc' })[0]).toBe(empty)
  })

  it('值相同时依次按创建时间、id 排，与输入顺序无关', () => {
    const a = playlist('p7', '收藏', 4, 100, 900)
    const b = playlist('p5', '收藏', 4, 200, 900)
    const c = playlist('p6', '收藏', 4, 200, 900)
    for (const key of ['name', 'updatedAt', 'trackCount'] as const) {
      expect(sortPlaylists([c, b, a], { key, order: 'asc' })).toEqual([a, b, c])
      expect(sortPlaylists([a, c, b], { key, order: 'asc' })).toEqual([a, b, c])
      expect(sortPlaylists([b, a, c], { key, order: 'desc' })).toEqual([c, b, a])
    }
  })

  it('不改动传入的数组', () => {
    const list = [commute, favorites, chill]
    sortPlaylists(list, { key: 'name', order: 'asc' })
    expect(list).toEqual([commute, favorites, chill])
  })
})
