import { describe, expect, it } from 'vitest'
import { reconnectDelay } from './backoff'

describe('reconnectDelay', () => {
  it.each([
    [1, 3000],
    [4, 24_000],
    [5, 30_000],
  ])('第 %i 次等待 %i 毫秒', (attempt, delay) => {
    expect(reconnectDelay(attempt)).toBe(delay)
  })
})
