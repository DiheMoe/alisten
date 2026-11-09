import { describe, expect, it } from 'vitest'
import { dropAt, moveByOne, moveTracks } from './track-order'

const order = ['a', 'b', 'c', 'd', 'e']

// 与 internal/domain/playlist/order_test.go 的 TestMove 逐条对应。
describe('moveTracks', () => {
  const cases: { name: string, moved: string[], before?: string, want?: string[], error?: string }[] = [
    { name: '移到最前', moved: ['d'], before: 'a', want: ['d', 'a', 'b', 'c', 'e'] },
    { name: '移到最后', moved: ['b'], want: ['a', 'c', 'd', 'e', 'b'] },
    { name: '移到中间', moved: ['a'], before: 'd', want: ['b', 'c', 'a', 'd', 'e'] },
    { name: '多首保持给定先后', moved: ['e', 'b'], before: 'd', want: ['a', 'c', 'e', 'b', 'd'] },
    { name: '多首移到最后保持给定先后', moved: ['c', 'a'], want: ['b', 'd', 'e', 'c', 'a'] },
    { name: '忽略不存在的歌', moved: ['x', 'c', 'y'], before: 'a', want: ['c', 'a', 'b', 'd', 'e'] },
    { name: '重复的歌只算一次', moved: ['c', 'c', 'b'], want: ['a', 'd', 'e', 'c', 'b'] },
    { name: 'before 紧跟在移动的歌之后', moved: ['b'], before: 'c', want: order },
    { name: 'before 是被移动的歌', moved: ['b', 'c'], before: 'c', error: 'before 不能是要移动的歌' },
    { name: 'before 是不存在的被移动的歌', moved: ['x'], before: 'x', error: 'before 不能是要移动的歌' },
    { name: 'before 不存在', moved: ['b'], before: 'x', error: '歌单已经变了，刷新之后再试' },
    { name: '空移动', moved: [], before: 'c', want: order },
    { name: '移动的歌都不存在', moved: ['x'], before: 'y', want: order },
  ]
  for (const { name, moved, before, want, error } of cases) {
    it(name, () => {
      const copy = [...order]
      if (error)
        expect(() => moveTracks(copy, moved, before)).toThrow(error)
      else
        expect(moveTracks(copy, moved, before)).toEqual(want)
      expect(copy).toEqual(order)
    })
  }
})

describe('dropAt', () => {
  it('插入位置之后第一首不移动的歌作为 before', () => {
    expect(dropAt(order, new Set(['d']), 1)).toEqual({ order: ['a', 'd', 'b', 'c', 'e'], moved: ['d'], before: 'b' })
  })

  it('插入位置紧跟着要移动的歌时，跳过它们找 before', () => {
    expect(dropAt(order, new Set(['b', 'e']), 1)).toEqual({ order: ['a', 'b', 'e', 'c', 'd'], moved: ['b', 'e'], before: 'c' })
  })

  it('放到最后时不传 before', () => {
    expect(dropAt(order, new Set(['a', 'c']), 5)).toEqual({ order: ['b', 'd', 'e', 'a', 'c'], moved: ['a', 'c'], before: undefined })
  })

  it('多首按当前顺序一起移动', () => {
    expect(dropAt(order, new Set(['e', 'b']), 0)).toEqual({ order: ['b', 'e', 'a', 'c', 'd'], moved: ['b', 'e'], before: 'a' })
  })

  it('拖回原位置不必发请求', () => {
    expect(dropAt(order, new Set(['c']), 2)).toBeNull()
    expect(dropAt(order, new Set(['c']), 3)).toBeNull()
    expect(dropAt(order, new Set(['b', 'c']), 2)).toBeNull()
  })

  it('全选时什么也不做', () => {
    expect(dropAt(order, new Set(order), 0)).toBeNull()
    expect(dropAt(order, new Set(order), 5)).toBeNull()
  })
})

describe('moveByOne', () => {
  it('上移和下移一首', () => {
    expect(moveByOne(order, new Set(['c']), -1)).toEqual({ order: ['a', 'c', 'b', 'd', 'e'], moved: ['c'], before: 'b' })
    expect(moveByOne(order, new Set(['c']), 1)).toEqual({ order: ['a', 'b', 'd', 'c', 'e'], moved: ['c'], before: 'e' })
  })

  it('下移到最后时不传 before', () => {
    expect(moveByOne(order, new Set(['d']), 1)).toEqual({ order: ['a', 'b', 'c', 'e', 'd'], moved: ['d'], before: undefined })
  })

  it('相连的多首一起移动', () => {
    expect(moveByOne(order, new Set(['b', 'c']), -1)).toEqual({ order: ['b', 'c', 'a', 'd', 'e'], moved: ['b', 'c'], before: 'a' })
    expect(moveByOne(order, new Set(['b', 'c']), 1)).toEqual({ order: ['a', 'd', 'b', 'c', 'e'], moved: ['b', 'c'], before: 'e' })
  })

  it('不相连的多首先收拢', () => {
    expect(moveByOne(order, new Set(['b', 'd']), -1)).toEqual({ order: ['b', 'd', 'a', 'c', 'e'], moved: ['b', 'd'], before: 'a' })
    expect(moveByOne(order, new Set(['b', 'd']), 1)).toEqual({ order: ['a', 'c', 'e', 'b', 'd'], moved: ['b', 'd'], before: undefined })
  })

  it('已经在最前时上移只收拢', () => {
    expect(moveByOne(order, new Set(['a', 'c']), -1)).toEqual({ order: ['a', 'c', 'b', 'd', 'e'], moved: ['a', 'c'], before: 'b' })
  })

  it('已经到头时不动', () => {
    expect(moveByOne(order, new Set(['a', 'b']), -1)).toBeNull()
    expect(moveByOne(order, new Set(['e']), 1)).toBeNull()
    expect(moveByOne(order, new Set(order), 1)).toBeNull()
  })

  it('要移动的歌都不在列表里时不动', () => {
    expect(moveByOne(order, new Set(['x']), -1)).toBeNull()
  })
})
