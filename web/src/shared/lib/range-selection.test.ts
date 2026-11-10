import { describe, expect, it } from 'vitest'
import { toggleSelection } from './range-selection'

const ids = ['a', 'b', 'c', 'd', 'e']

describe('toggleSelection', () => {
  it('普通点击切换单个 id，并把它设为锚点', () => {
    expect(toggleSelection(new Set(), ids, 'b', null, false)).toEqual({ selected: new Set(['b']), anchor: 'b' })
    expect(toggleSelection(new Set(['b', 'c']), ids, 'b', 'c', false)).toEqual({ selected: new Set(['c']), anchor: 'b' })
  })

  it('按住 Shift 点击时选中锚点到当前 id 之间的全部，含两端，方向不限', () => {
    expect(toggleSelection(new Set(['b']), ids, 'd', 'b', true)).toEqual({ selected: new Set(['b', 'c', 'd']), anchor: 'd' })
    expect(toggleSelection(new Set(['d']), ids, 'a', 'd', true)).toEqual({ selected: new Set(['a', 'b', 'c', 'd']), anchor: 'a' })
  })

  it('按住 Shift 点击时保留范围之外已经选中的 id', () => {
    expect(toggleSelection(new Set(['a', 'c']), ids, 'e', 'c', true)).toEqual({ selected: new Set(['a', 'c', 'd', 'e']), anchor: 'e' })
  })

  it('没有锚点时的 Shift 点击等同于普通点击', () => {
    expect(toggleSelection(new Set(), ids, 'c', null, true)).toEqual({ selected: new Set(['c']), anchor: 'c' })
    expect(toggleSelection(new Set(['c']), ids, 'c', null, true)).toEqual({ selected: new Set(), anchor: 'c' })
  })

  it('锚点不在当前列表里时的 Shift 点击等同于普通点击', () => {
    expect(toggleSelection(new Set(), ids, 'c', 'z', true)).toEqual({ selected: new Set(['c']), anchor: 'c' })
  })

  it('不修改传入的集合', () => {
    const selected = new Set(['a'])
    toggleSelection(selected, ids, 'c', 'a', true)
    expect(selected).toEqual(new Set(['a']))
  })
})
