import type { PlayRecord, SearchCategory, SearchRecord, SearchSource } from './records'
import { describe, expect, it } from 'vitest'
import { parsePlays, parseSearches, playHistoryLimit, recordPlay, recordSearch } from './records'

function play(id: string, playedAtMs: number, source: PlayRecord['track']['source'] = 'netease'): PlayRecord {
  return {
    track: { source, id },
    meta: { title: id, artist: '', album: '', coverUrl: '', pageUrl: '', durationMs: 1000 },
    playedAtMs,
  }
}

function search(query: string, searchedAtMs: number, source: SearchSource = 'netease', category: SearchCategory = 'all'): SearchRecord {
  return { query, source, category, searchedAtMs }
}

describe('recordPlay', () => {
  it('新的记录排在最前，同一首歌只留最近一次', () => {
    const plays = recordPlay([play('2', 200), play('1', 100)], play('1', 300))
    expect(plays).toEqual([play('1', 300), play('2', 200)])
  })

  it('来源不同的同一个 ID 是两首歌', () => {
    expect(recordPlay([play('1', 100, 'qq')], play('1', 200))).toEqual([play('1', 200), play('1', 100, 'qq')])
  })

  it('最多保留 500 条', () => {
    const plays = Array.from({ length: playHistoryLimit }, (_, i) => play(String(i), i))
    const next = recordPlay(plays, play('new', 1000))
    expect(next).toHaveLength(500)
    expect(next[0]).toEqual(play('new', 1000))
    expect(next[499]).toEqual(play('498', 498))
  })
})

describe('parsePlays', () => {
  it('保留 B 站的播放记录', () => {
    const record = play('BV1xx411c7mD/2', 1, 'bilibili')
    expect(parsePlays(JSON.stringify([record]))).toEqual([record])
  })

  it('丢弃不完整的记录', () => {
    const broken = [
      { track: { source: 'netease', id: '2' }, playedAtMs: 1 },
      { ...play('3', 3), track: { source: 'other', id: '3' } },
      { ...play('4', 4), meta: { title: '4', artist: '', album: '', coverUrl: '', durationMs: 1 } },
      null,
    ]
    expect(parsePlays(JSON.stringify([play('1', 1), ...broken]))).toEqual([play('1', 1)])
  })

  it('没有记录或者内容无法解析时为空', () => {
    expect(parsePlays(null)).toEqual([])
    expect(parsePlays('{')).toEqual([])
    expect(parsePlays('{}')).toEqual([])
  })
})

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
