import type { SearchRecord } from './records'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeStorage } from '@/core/api/fake-storage'

const searchesKey = 'alisten.search-history'
const searchesEnabledKey = 'alisten.search-history.enabled'
const searchLimitKey = 'alisten.search-history.limit'

let local: FakeStorage
let tab: EventTarget

function search(query: string, searchedAtMs: number): SearchRecord {
  return { query, source: 'netease', category: 'all', searchedAtMs }
}

function saved(): unknown {
  return JSON.parse(local.getItem(searchesKey) ?? 'null')
}

// 每次调用都重新加载模块，相当于重新打开页面。localStorage 里的内容保留。
async function opened() {
  vi.resetModules()
  const { searchHistoryStore } = await import('./search-history-store')
  searchHistoryStore.start()
  return searchHistoryStore
}

type SearchHistoryStore = Awaited<ReturnType<typeof opened>>

function record(store: SearchHistoryStore, ...queries: string[]): void {
  for (const query of queries)
    store.recordSearch(query, 'netease', 'all')
}

function queries(store: SearchHistoryStore): string[] {
  return store.state.searches.map(({ query }) => query)
}

// 其他标签页写入之后，localStorage 里已经是新的值，本页另外收到一次 storage 事件。
function writtenElsewhere(key: string, value: string): void {
  local.setItem(key, value)
  tab.dispatchEvent(Object.assign(new Event('storage'), { key, newValue: value }))
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'], now: 1000 })
  local = new FakeStorage()
  tab = new EventTarget()
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('window', tab)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('记录搜索', () => {
  it('记下搜索的时间，放到最前并保存，同一个查询只留最近的一条', async () => {
    const store = await opened()
    store.recordSearch('晴天', 'netease', 'all')
    vi.setSystemTime(2000)
    store.recordSearch('Jay', 'qq', 'tracks')
    vi.setSystemTime(3000)
    store.recordSearch(' 晴天 ', 'qq', 'playlists')
    expect(store.state.searches).toEqual([
      { query: '晴天', source: 'qq', category: 'playlists', searchedAtMs: 3000 },
      { query: 'Jay', source: 'qq', category: 'tracks', searchedAtMs: 2000 },
    ])
    expect(saved()).toEqual(store.state.searches)
  })

  it('超过上限时丢弃最旧的', async () => {
    local.setItem(searchLimitKey, '5')
    const store = await opened()
    record(store, 'a', 'b', 'c', 'd', 'e', 'f')
    expect(queries(store)).toEqual(['f', 'e', 'd', 'c', 'b'])
    expect(saved()).toEqual(store.state.searches)
  })

  it('关掉保存搜索记录之后不再记录，已有的记录不动', async () => {
    const store = await opened()
    record(store, 'a')
    store.updateSettings({ searchesEnabled: false, searchLimit: 10 })
    record(store, 'b')
    expect(store.state.searches).toEqual([search('a', 1000)])
    expect(saved()).toEqual([search('a', 1000)])
  })

  it('清空', async () => {
    const store = await opened()
    record(store, 'a', 'b')
    store.clearSearches()
    expect(store.state.searches).toEqual([])
    expect(saved()).toEqual([])
  })
})

describe('开关和上限', () => {
  it('没有保存过时开着，上限是 10 条', async () => {
    const store = await opened()
    expect(store.state).toEqual({ searches: [], searchesEnabled: true, searchLimit: 10 })
  })

  it('保存下来，重新打开页面后恢复', async () => {
    const store = await opened()
    store.updateSettings({ searchesEnabled: false, searchLimit: 20 })
    expect(store.state).toMatchObject({ searchesEnabled: false, searchLimit: 20 })
    expect(local.getItem(searchesEnabledKey)).toBe('false')
    expect(local.getItem(searchLimitKey)).toBe('20')
    expect((await opened()).state).toMatchObject({ searchesEnabled: false, searchLimit: 20 })
  })

  it('保存的上限不在可选的范围内时用 10 条', async () => {
    local.setItem(searchLimitKey, '7')
    const store = await opened()
    expect(store.state.searchLimit).toBe(10)
  })

  it('调小上限时截掉多出的记录并保存，之后按新的上限记录', async () => {
    const store = await opened()
    record(store, 'a', 'b', 'c', 'd', 'e', 'f')
    store.updateSettings({ searchesEnabled: true, searchLimit: 5 })
    expect(queries(store)).toEqual(['f', 'e', 'd', 'c', 'b'])
    expect(saved()).toEqual(store.state.searches)
    record(store, 'g')
    expect(queries(store)).toEqual(['g', 'f', 'e', 'd', 'c'])
  })
})

describe('恢复保存的记录', () => {
  it('重新打开页面后记录还在', async () => {
    const store = await opened()
    record(store, 'a')
    vi.setSystemTime(2000)
    record(store, 'b')
    expect((await opened()).state.searches).toEqual([search('b', 2000), search('a', 1000)])
  })

  it('有无效的记录或者超过上限时，只恢复有效的、上限以内的，并把整理后的结果写回', async () => {
    local.setItem(searchLimitKey, '5')
    local.setItem(searchesKey, JSON.stringify([search('g', 7), { query: 'x' }, search('f', 6), search('e', 5), search('d', 4), search('c', 3), search('b', 2), search('a', 1)]))
    const store = await opened()
    expect(queries(store)).toEqual(['g', 'f', 'e', 'd', 'c'])
    expect(saved()).toEqual(store.state.searches)
  })

  it('内容无法解析时当作没有记录，并写回空的记录', async () => {
    local.setItem(searchesKey, '[')
    const store = await opened()
    expect(store.state.searches).toEqual([])
    expect(saved()).toEqual([])
  })
})

describe('其他标签页的写入', () => {
  it('写入搜索记录时本页跟着更新，本页之后的写入不会覆盖那个标签页写入的记录', async () => {
    const store = await opened()
    record(store, 'a')
    const theirs = [search('b', 2000), search('a', 1000)]
    writtenElsewhere(searchesKey, JSON.stringify(theirs))
    expect(store.state.searches).toEqual(theirs)
    vi.setSystemTime(3000)
    record(store, 'c')
    expect(saved()).toEqual([search('c', 3000), ...theirs])
  })

  it('写入别的键时搜索记录不动', async () => {
    const store = await opened()
    record(store, 'a')
    writtenElsewhere('alisten.member-token', 'token')
    expect(store.state.searches).toEqual([search('a', 1000)])
  })
})
