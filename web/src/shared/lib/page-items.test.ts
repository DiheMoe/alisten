import { describe, expect, it } from 'vitest'
import { clampPage, pageItems } from './page-items'

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

describe('clampPage', () => {
  it('范围内的页码原样返回', () => {
    expect(clampPage(2, 25, 10)).toBe(2)
    expect(clampPage(3, 25, 10)).toBe(3)
  })

  it('超出的页码落到最后一页，小于 1 的落到第 1 页', () => {
    expect(clampPage(999, 25, 10)).toBe(3)
    expect(clampPage(4, 30, 10)).toBe(3)
    expect(clampPage(0, 25, 10)).toBe(1)
  })

  it('没有内容时是第 1 页', () => {
    expect(clampPage(5, 0, 10)).toBe(1)
  })
})
