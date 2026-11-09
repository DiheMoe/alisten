import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { loadViewPreference, saveViewPreference } from './view-preference'

const key = 'alisten.playlists-view'
let items: Map<string, string>

beforeEach(() => {
  items = new Map()
  vi.stubGlobal('localStorage', {
    getItem: (name: string) => items.get(name) ?? null,
    setItem: (name: string, value: string) => items.set(name, value),
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const fallback = { view: 'grid', sort: { key: 'updatedAt', order: 'desc' } }

describe('视图偏好', () => {
  it('没有保存过时用网格视图，按更新时间、最新在前', () => {
    expect(loadViewPreference()).toEqual(fallback)
  })

  it('保存之后读到同样的偏好', () => {
    const preference = { view: 'list', sort: { key: 'name', order: 'asc' } } as const
    saveViewPreference(preference)
    expect(loadViewPreference()).toEqual(preference)
  })

  it('不是 JSON 时回退默认', () => {
    items.set(key, '{')
    expect(loadViewPreference()).toEqual(fallback)
    items.set(key, 'null')
    expect(loadViewPreference()).toEqual(fallback)
  })

  it('无效的字段各自回退默认', () => {
    items.set(key, JSON.stringify({ view: 'list', sort: { key: 'size', order: 'asc' } }))
    expect(loadViewPreference()).toEqual({ view: 'list', sort: fallback.sort })
    items.set(key, JSON.stringify({ view: 'columns', sort: { key: 'trackCount', order: 'asc' } }))
    expect(loadViewPreference()).toEqual({ view: 'grid', sort: { key: 'trackCount', order: 'asc' } })
    items.set(key, JSON.stringify({ view: 'list', sort: { key: 'name', order: 'up' } }))
    expect(loadViewPreference()).toEqual({ view: 'list', sort: fallback.sort })
    items.set(key, JSON.stringify({ view: 'list', sort: 'name' }))
    expect(loadViewPreference()).toEqual({ view: 'list', sort: fallback.sort })
  })
})
