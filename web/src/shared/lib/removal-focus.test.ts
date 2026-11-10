import { describe, expect, it } from 'vitest'
import { rowAfterRemoval } from './removal-focus'

describe('rowAfterRemoval', () => {
  it('删掉一行之后落到原来的下一行', () => {
    expect(rowAfterRemoval(['a', 'b', 'c', 'd'], ['a', 'c', 'd'], ['b'])).toBe('c')
  })

  it('删掉第一行之后落到新的第一行', () => {
    expect(rowAfterRemoval(['a', 'b', 'c'], ['b', 'c'], ['a'])).toBe('b')
  })

  it('删掉这一页的最后一行、下一页的行补上来时，落到补上来的那一行', () => {
    expect(rowAfterRemoval(['a', 'b', 'c'], ['a', 'b', 'x'], ['c'])).toBe('x')
  })

  it('删掉的是最后一行、后面没有行了，返回 null', () => {
    expect(rowAfterRemoval(['a', 'b', 'c'], ['a', 'b'], ['c'])).toBeNull()
  })

  it('这一页全删空了，返回 null', () => {
    expect(rowAfterRemoval(['a', 'b'], [], ['a', 'b'])).toBeNull()
  })

  it('一次删掉不相邻的几行，落到删掉的第一行原来的下一行', () => {
    expect(rowAfterRemoval(['a', 'b', 'c', 'd', 'e'], ['a', 'c', 'e'], ['b', 'd'])).toBe('c')
  })

  it('一次删掉相邻的几行，落到它们后面的那一行', () => {
    expect(rowAfterRemoval(['a', 'b', 'c', 'd'], ['a', 'd'], ['b', 'c'])).toBe('d')
  })

  it('要删的行有的没删掉（比如正在播放），从真正删掉的第一行算起', () => {
    expect(rowAfterRemoval(['a', 'b', 'c', 'd'], ['a', 'b', 'd'], ['b', 'c'])).toBe('d')
  })

  it('一行也没删掉，返回 null', () => {
    expect(rowAfterRemoval(['a', 'b'], ['a', 'b'], ['b'])).toBeNull()
  })

  it('前面插进了新的行也不影响', () => {
    expect(rowAfterRemoval(['a', 'b', 'c'], ['n', 'a', 'c'], ['b'])).toBe('c')
  })
})
