import { describe, expect, it } from 'vitest'
import { parseCapacity } from './room-capacity'

describe('parseCapacity', () => {
  it('接受 2 到 128 之间的整数', () => {
    expect(parseCapacity('2')).toBe(2)
    expect(parseCapacity('64')).toBe(64)
    expect(parseCapacity('128')).toBe(128)
  })

  it('超出范围、不是整数或者没填时不能保存', () => {
    expect(parseCapacity('1')).toBeNull()
    expect(parseCapacity('129')).toBeNull()
    expect(parseCapacity('16.5')).toBeNull()
    expect(parseCapacity('')).toBeNull()
  })
})
