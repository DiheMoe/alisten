import { afterEach, describe, expect, it, vi } from 'vitest'
import { reconnectDelay } from './backoff'

describe('reconnectDelay', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it.each([
    [1, 0.5, 3000],
    [4, 0.5, 24_000],
    [5, 0.5, 30_000],
    [1, 0, 2250],
    [5, 0, 22_500],
    [5, 1, 37_500],
  ])('第 %i 次、随机数为 %d 时等待 %i 毫秒', (attempt, random, delay) => {
    vi.spyOn(Math, 'random').mockReturnValue(random)
    expect(reconnectDelay(attempt)).toBe(delay)
  })
})
