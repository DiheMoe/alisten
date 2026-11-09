import type { Selection } from './track-selection'
import { describe, expect, it } from 'vitest'
import { noSelection, retain, selectAll, selectRange, toggle } from './track-selection'

const order = ['a', 'b', 'c', 'd', 'e']

function keys(selection: Selection): string[] {
  return [...selection.keys].sort()
}

describe('track-selection', () => {
  it('单击和 ⌘ 点击逐首切换，并把这首作为锚点', () => {
    const one = toggle(noSelection, 'b')
    expect(keys(one)).toEqual(['b'])
    expect(one.anchor).toBe('b')
    const two = toggle(one, 'd')
    expect(keys(two)).toEqual(['b', 'd'])
    const back = toggle(two, 'b')
    expect(keys(back)).toEqual(['d'])
    expect(back.anchor).toBe('b')
  })

  it('shift 点击把锚点到这首的一段加入选择，锚点不变', () => {
    const selection = selectRange(toggle(toggle(noSelection, 'e'), 'b'), order, 'd')
    expect(keys(selection)).toEqual(['b', 'c', 'd', 'e'])
    expect(selection.anchor).toBe('b')
  })

  it('shift 点击锚点之前的歌时向上选', () => {
    expect(keys(selectRange(toggle(noSelection, 'd'), order, 'b'))).toEqual(['b', 'c', 'd'])
  })

  it('没有锚点时 shift 点击只切换这首', () => {
    const selection = selectRange(noSelection, order, 'c')
    expect(keys(selection)).toEqual(['c'])
    expect(selection.anchor).toBe('c')
  })

  it('锚点已经不在列表里时 shift 点击只切换这首', () => {
    expect(keys(selectRange(toggle(noSelection, 'x'), order, 'c'))).toEqual(['c', 'x'])
  })

  it('全选和清空', () => {
    expect(keys(selectAll(order))).toEqual(order)
    expect(keys(noSelection)).toEqual([])
  })

  it('列表变化后去掉不存在的歌和锚点', () => {
    const selection = retain(toggle(toggle(noSelection, 'a'), 'c'), ['a', 'b'])
    expect(keys(selection)).toEqual(['a'])
    expect(selection.anchor).toBeNull()
  })

  it('列表变化不影响选择时返回原对象', () => {
    const selection = toggle(noSelection, 'c')
    expect(retain(selection, order)).toBe(selection)
  })
})
