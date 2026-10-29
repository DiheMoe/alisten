import type { Source, Track, TrackMeta } from '@/core/realtime'

export interface PlayRecord {
  track: Track
  meta: TrackMeta
  playedAtMs: number
}

export type SearchSource = 'netease' | 'qq'
export type SearchCategory = 'all' | 'tracks' | 'playlists'

export interface SearchRecord {
  query: string
  source: SearchSource
  category: SearchCategory
  searchedAtMs: number
}

export const playHistoryLimit = 500

const trackSources: unknown[] = ['netease', 'qq', 'link', 'bilibili'] satisfies Source[]
const searchSources: unknown[] = ['netease', 'qq'] satisfies SearchSource[]
const searchCategories: unknown[] = ['all', 'tracks', 'playlists'] satisfies SearchCategory[]
const metaText = ['title', 'artist', 'album', 'coverUrl', 'pageUrl'] as const

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

function isPlayRecord(value: unknown): value is PlayRecord {
  if (!isObject(value) || !isObject(value.track) || !isObject(value.meta) || typeof value.playedAtMs !== 'number')
    return false
  const { track, meta } = value
  return trackSources.includes(track.source) && typeof track.id === 'string'
    && metaText.every(key => typeof meta[key] === 'string') && typeof meta.durationMs === 'number'
}

function isSearchRecord(value: unknown): value is SearchRecord {
  return isObject(value) && typeof value.query === 'string' && typeof value.searchedAtMs === 'number'
    && searchSources.includes(value.source) && searchCategories.includes(value.category)
}

export function parsePlays(stored: string | null): PlayRecord[] {
  return readList(stored).filter(isPlayRecord).slice(0, playHistoryLimit)
}

export function recordPlay(plays: PlayRecord[], record: PlayRecord): PlayRecord[] {
  const rest = plays.filter(play => play.track.source !== record.track.source || play.track.id !== record.track.id)
  return [record, ...rest].slice(0, playHistoryLimit)
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
