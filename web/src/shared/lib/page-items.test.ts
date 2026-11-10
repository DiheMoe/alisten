import { describe, expect, it } from 'vitest'
import { pageItems } from './page-items'

describe('pageItems', () => {
  it('页数不多时列出全部页码', () => {
    expect(pageItems(1, 1)).toEqual([1])
    expect(pageItems(1, 5)).toEqual([1, 2, 3, 4, 5])
    expect(pageItems(4, 7)).toEqual([1, 2, 3, 4, 5, 6, 7])
  })

  it('页数多时只列出首页、末页和当前页前后各一页，其余用省略号', () => {
    expect(pageItems(2, 26)).toEqual([1, 2, 3, 'gap', 26])
    expect(pageItems(13, 26)).toEqual([1, 'gap', 12, 13, 14, 'gap', 26])
    expect(pageItems(26, 26)).toEqual([1, 'gap', 25, 26])
  })

  it('省略号只盖住一页时直接显示那一页', () => {
    expect(pageItems(4, 8)).toEqual([1, 2, 3, 4, 5, 'gap', 8])
    expect(pageItems(5, 8)).toEqual([1, 'gap', 4, 5, 6, 7, 8])
  })
})
