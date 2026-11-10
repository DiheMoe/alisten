import type { SearchCategory, SearchRecord, SearchSource } from './records'
import { describe, expect, it } from 'vitest'
import { parseSearches, recordSearch } from './records'

function search(query: string, searchedAtMs: number, source: SearchSource = 'netease', category: SearchCategory = 'all'): SearchRecord {
  return { query, source, category, searchedAtMs }
}

describe('recordSearch', () => {
  it('不区分大小写和首尾空白，同一个查询只留最近的一条，平台和类别取最近一次', () => {
    const searches = recordSearch([search('晴天', 1), search('Jay', 2, 'qq', 'tracks')], search(' jay ', 3, 'netease', 'playlists'), 10)
    expect(searches).toEqual([search('jay', 3, 'netease', 'playlists'), search('晴天', 1)])
  })

  it('超过上限时丢弃最旧的', () => {
    expect(recordSearch([search('b', 2), search('a', 1)], search('c', 3), 2)).toEqual([search('c', 3), search('b', 2)])
  })
})

describe('parseSearches', () => {
  it('合并重复的查询，保留最新的一条，按时间从新到旧排列', () => {
    const stored = JSON.stringify([search('Jay', 1, 'qq'), search('晴天', 2), search(' jay', 3, 'netease', 'tracks')])
    expect(parseSearches(stored, 10)).toEqual([search('jay', 3, 'netease', 'tracks'), search('晴天', 2)])
  })

  it('丢弃无效的记录，只保留上限以内的', () => {
    const stored = JSON.stringify([search('a', 1), search(' ', 2), { ...search('b', 3), source: 'link' }, { query: 'c' }, search('d', 4), search('e', 5)])
    expect(parseSearches(stored, 2)).toEqual([search('e', 5), search('d', 4)])
  })

  it('没有记录或者内容无法解析时为空', () => {
    expect(parseSearches(null, 10)).toEqual([])
    expect(parseSearches('[', 10)).toEqual([])
  })
})
