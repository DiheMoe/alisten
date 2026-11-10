import { describe, expect, it } from 'vitest'
import { rovingIndex } from './roving'

describe('rovingIndex', () => {
  it('← 和 ↑ 去上一项，→ 和 ↓ 去下一项，到头之后从另一头接着走', () => {
    expect(rovingIndex('ArrowLeft', 1, 3)).toBe(0)
    expect(rovingIndex('ArrowUp', 1, 3)).toBe(0)
    expect(rovingIndex('ArrowRight', 1, 3)).toBe(2)
    expect(rovingIndex('ArrowDown', 1, 3)).toBe(2)
    expect(rovingIndex('ArrowUp', 0, 3)).toBe(2)
    expect(rovingIndex('ArrowDown', 2, 3)).toBe(0)
  })

  it('按 Home 和 End 去第一项和最后一项', () => {
    expect(rovingIndex('Home', 2, 3)).toBe(0)
    expect(rovingIndex('End', 0, 3)).toBe(2)
  })

  it('其他按键和空的组不处理', () => {
    expect(rovingIndex('Enter', 1, 3)).toBeNull()
    expect(rovingIndex(' ', 1, 3)).toBeNull()
    expect(rovingIndex('ArrowDown', 0, 0)).toBeNull()
  })
})
