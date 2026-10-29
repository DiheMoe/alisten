import { describe, expect, it } from 'vitest'
import { Clock } from './clock'

function manualClock(start = 1000) {
  let now = start
  return {
    clock: new Clock(() => now),
    advance: (ms: number) => {
      now += ms
    },
  }
}

describe('clock', () => {
  it('没有样本时不知道服务器时间', () => {
    const { clock } = manualClock()
    expect(clock.offsetMs).toBeNull()
    expect(clock.serverNow()).toBeNull()
  })

  it('偏移等于服务器时间减去发送与收到时间的中点', () => {
    const { clock, advance } = manualClock(1000)
    clock.addSample(1000, 1100, 5050)
    expect(clock.offsetMs).toBe(4000)
    advance(500)
    expect(clock.serverNow()).toBe(5500)
  })

  it('取往返时间最小的样本', () => {
    const { clock } = manualClock()
    clock.addSample(1000, 1300, 6150)
    clock.addSample(2000, 2040, 7020)
    clock.addSample(3000, 3200, 9100)
    expect(clock.offsetMs).toBe(5000)
  })

  it('只看最近 12 个样本', () => {
    const { clock } = manualClock()
    clock.addSample(0, 10, 1005)
    for (let i = 1; i <= 11; i++)
      clock.addSample(i * 1000, i * 1000 + 100, i * 1000 + 50 + 2000)
    expect(clock.offsetMs).toBe(1000)
    clock.addSample(12_000, 12_100, 12_050 + 2000)
    expect(clock.offsetMs).toBe(2000)
  })

  it('清空后丢弃样本，也不接受清空前发出的 ping', () => {
    const { clock, advance } = manualClock(1000)
    clock.addSample(1000, 1100, 5050)
    advance(1000)
    clock.reset()
    expect(clock.offsetMs).toBeNull()
    clock.addSample(1500, 2100, 9000)
    expect(clock.offsetMs).toBeNull()
    clock.addSample(2000, 2100, 7050)
    expect(clock.offsetMs).toBe(5000)
  })
})
