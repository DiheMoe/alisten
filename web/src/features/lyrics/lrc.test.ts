import { describe, expect, it } from 'vitest'
import { lineIndexAt, parseLrc } from './lrc'

describe('parseLrc', () => {
  it('解析分、秒和不同精度的小数部分', () => {
    expect(parseLrc('[00:12.34]晴天\n[01:02]刮风这天\n[00:01.5]a\n[00:02.123]b')).toEqual([
      { timeMs: 1500, text: 'a' },
      { timeMs: 2123, text: 'b' },
      { timeMs: 12_340, text: '晴天' },
      { timeMs: 62_000, text: '刮风这天' },
    ])
  })

  it('一行有多个时间标签时，每个标签各生成一行', () => {
    expect(parseLrc('[00:05.00][00:01.00] 副歌 ')).toEqual([
      { timeMs: 1000, text: '副歌' },
      { timeMs: 5000, text: '副歌' },
    ])
  })

  it('忽略没有时间标签的行，连续的空白行只保留一行', () => {
    expect(parseLrc('[ar:周杰伦]\n[00:01.00]一\n[00:02.00]\n[00:03.00]  \n[00:04.00]二\r\n[00:05.00]')).toEqual([
      { timeMs: 1000, text: '一' },
      { timeMs: 2000, text: '' },
      { timeMs: 4000, text: '二' },
      { timeMs: 5000, text: '' },
    ])
  })
})

describe('lineIndexAt', () => {
  const lines = [{ timeMs: 1000, text: '一' }, { timeMs: 2000, text: '二' }, { timeMs: 3000, text: '三' }]

  it('返回开始时间不晚于当前位置的最后一行', () => {
    expect(lineIndexAt(lines, 0)).toBe(-1)
    expect(lineIndexAt(lines, 1000)).toBe(0)
    expect(lineIndexAt(lines, 2999)).toBe(1)
    expect(lineIndexAt(lines, 60_000)).toBe(2)
    expect(lineIndexAt([], 1000)).toBe(-1)
  })
})
