import type { PlaylistSort } from './playlist-sort'
import { sortKeys, sortOrders } from './playlist-sort'

export type PlaylistView = 'list' | 'grid'

export interface ViewPreference {
  view: PlaylistView
  sort: PlaylistSort
}

const preferenceKey = 'alisten.playlists-view'
const views: PlaylistView[] = ['list', 'grid']
const defaultPreference: ViewPreference = { view: 'grid', sort: { key: 'updatedAt', order: 'desc' } }

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function readStored(): Record<string, unknown> {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(preferenceKey) ?? 'null')
    return isObject(value) ? value : {}
  }
  catch {
    return {}
  }
}

export function loadViewPreference(): ViewPreference {
  const stored = readStored()
  const sort = isObject(stored.sort) ? stored.sort : {}
  const key = sortKeys.find(k => k === sort.key)
  const order = sortOrders.find(o => o === sort.order)
  return {
    view: views.find(v => v === stored.view) ?? defaultPreference.view,
    sort: key && order ? { key, order } : defaultPreference.sort,
  }
}

export function saveViewPreference(preference: ViewPreference): void {
  localStorage.setItem(preferenceKey, JSON.stringify(preference))
}
