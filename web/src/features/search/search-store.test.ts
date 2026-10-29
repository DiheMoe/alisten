import { describe, expect, it } from 'vitest'
import { cachedResult, nextLoad, rememberResult } from './search-store'

describe('nextLoad', () => {
  it('条件没变时沿用当前结果，强制刷新时绕过缓存重新请求', () => {
    expect(nextLoad('a', 'a', true, true, false)).toBeNull()
    expect(nextLoad('a', 'a', true, true, true)).toEqual({ shown: 'a', loading: true, from: 'request' })
  })

  it('没有关键词时显示最近搜索，不发出请求', () => {
    expect(nextLoad(null, 'a', false, true, false)).toEqual({ shown: null, loading: false, from: 'none' })
  })

  it('回到已经显示过的条件时直接用缓存，不必连着房间', () => {
    expect(nextLoad('a', 'b', true, false, false)).toEqual({ shown: 'a', loading: false, from: 'cache' })
  })

  it('没连上房间时推迟请求并让当前结果作废，连上之后回到原来的条件也会重新请求', () => {
    expect(nextLoad('a', 'b', false, false, false)).toEqual({ shown: null, loading: true, from: 'none' })
    expect(nextLoad('b', null, false, false, false)).toEqual({ shown: null, loading: true, from: 'none' })
    expect(nextLoad('b', null, false, true, false)).toEqual({ shown: 'b', loading: true, from: 'request' })
  })
})

describe('搜索结果的缓存', () => {
  const minute = 60 * 1000

  it('取回超过 10 分钟的结果失效', () => {
    const cache = rememberResult(new Map(), 'a', { value: 1, at: 0 }, 0)
    expect(cachedResult(cache, 'a', 10 * minute - 1)?.value).toBe(1)
    expect(cachedResult(cache, 'a', 10 * minute)).toBeNull()
    expect(cachedResult(cache, 'b', 0)).toBeNull()
  })

  it('最多保留 10 份，丢掉最久没显示的；再次显示的移到最近', () => {
    let cache = new Map<string, { value: number, at: number }>()
    for (let i = 0; i < 10; i++)
      cache = rememberResult(cache, `k${i}`, { value: i, at: i }, i)
    cache = rememberResult(cache, 'k0', { value: 0, at: 0 }, 10)
    cache = rememberResult(cache, 'k10', { value: 10, at: 11 }, 11)
    expect([...cache.keys()]).toEqual(['k2', 'k3', 'k4', 'k5', 'k6', 'k7', 'k8', 'k9', 'k0', 'k10'])
  })

  it('记下新结果时去掉已经失效的', () => {
    const cache = rememberResult(new Map([['old', { value: 1, at: 0 }]]), 'new', { value: 2, at: 10 * minute }, 10 * minute)
    expect([...cache.keys()]).toEqual(['new'])
  })
})
