import type { MyPlaylist } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { arrange, savedMessage, saveOutcome } from './save-result'

function playlist(id: string, updatedAtMs: number): MyPlaylist {
  return { id, name: id, favorites: false, trackCount: 0, createdAtMs: 1, updatedAtMs, coverUrl: null, shareId: null }
}

const ids = (list: readonly MyPlaylist[]) => list.map(p => p.id).join('')

describe('arrange', () => {
  it('按打开对话框时的先后排，更新时间变了也不动', () => {
    expect(ids(arrange([playlist('a', 9), playlist('b', 5), playlist('c', 1)], ['c', 'b', 'a']))).toBe('cba')
  })

  it('打开之后才出现的歌单排在最后，它们之间按更新时间从新到旧', () => {
    expect(ids(arrange([playlist('old', 2), playlist('a', 1), playlist('new', 8)], ['a']))).toBe('anewold')
  })
})

describe('saveOutcome', () => {
  it('只算保存成功的歌单，已经在里面的首数按歌单累加', () => {
    const [a, b, c] = [playlist('a', 1), playlist('b', 1), playlist('c', 1)]
    expect(saveOutcome([a, b, c], [3, null, 1], 3)).toEqual({ saved: [a, c], skipped: 2 })
  })

  it('都失败时没有保存成功的歌单', () => {
    expect(saveOutcome([playlist('a', 1)], [null], 3)).toEqual({ saved: [], skipped: 0 })
  })
})

describe('savedMessage', () => {
  it('一个歌单时写出歌单名，多个时写个数', () => {
    expect(savedMessage([playlist('通勤', 1)], 0)).toBe('已保存到“通勤”')
    expect(savedMessage([playlist('a', 1), playlist('b', 1)], 0)).toBe('已保存到 2 个歌单')
  })

  it('有歌已经在里面时说明首数', () => {
    expect(savedMessage([playlist('通勤', 1)], 2)).toBe('已保存到“通勤”，其中 2 首已经在里面')
  })
})
