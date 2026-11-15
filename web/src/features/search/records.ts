// 接口的 source 字段收全部 Source，但服务端只能在这几个音源里搜索、读取歌单内的歌曲。搜索页按这里的顺序列出它们。
export const searchSources = ['qq', 'netease'] as const
export type SearchSource = typeof searchSources[number]
export const searchSourceNames: Record<SearchSource, string> = { qq: 'QQ 音乐', netease: '网易云音乐' }

export type SearchCategory = 'all' | 'tracks' | 'playlists'

export interface SearchRecord {
  query: string
  source: SearchSource
  category: SearchCategory
  searchedAtMs: number
}

const searchCategories: unknown[] = ['all', 'tracks', 'playlists'] satisfies SearchCategory[]

export function isSearchSource(value: unknown): value is SearchSource {
  return typeof value === 'string' && Object.hasOwn(searchSourceNames, value)
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function readList(stored: string | null): unknown[] {
  try {
    const value: unknown = JSON.parse(stored ?? '[]')
    return Array.isArray(value) ? value : []
  }
  catch {
    return []
  }
}

function isSearchRecord(value: unknown): value is SearchRecord {
  return isObject(value) && typeof value.query === 'string' && typeof value.searchedAtMs === 'number'
    && isSearchSource(value.source) && searchCategories.includes(value.category)
}

function queryKey(query: string): string {
  return query.trim().toLowerCase()
}

export function recordSearch(searches: SearchRecord[], record: SearchRecord, limit: number): SearchRecord[] {
  const key = queryKey(record.query)
  return [{ ...record, query: record.query.trim() }, ...searches.filter(search => queryKey(search.query) !== key)].slice(0, limit)
}

export function parseSearches(stored: string | null, limit: number): SearchRecord[] {
  const latest = new Map<string, SearchRecord>()
  for (const item of readList(stored)) {
    if (!isSearchRecord(item) || !queryKey(item.query))
      continue
    const key = queryKey(item.query)
    const previous = latest.get(key)
    if (!previous || item.searchedAtMs > previous.searchedAtMs)
      latest.set(key, { query: item.query.trim(), source: item.source, category: item.category, searchedAtMs: item.searchedAtMs })
  }
  return [...latest.values()].sort((a, b) => b.searchedAtMs - a.searchedAtMs).slice(0, limit)
}
