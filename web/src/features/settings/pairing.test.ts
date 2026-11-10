import { describe, expect, it } from 'vitest'
import { countdownText, formatPairingCode, isPairingCode, normalizePairingCode, secondsLeft } from './pairing'

describe('normalizePairingCode', () => {
  it.each([
    ['abcd2345', 'ABCD2345'],
    [' ABCD-2345 ', 'ABCD2345'],
    ['abcd 2345', 'ABCD2345'],
    ['ABCD%2D2345', 'ABCD2345'],
    ['%E0%A4%A', '%E0%A4%A'],
  ])('%s → %s', (input, code) => {
    expect(normalizePairingCode(input)).toBe(code)
  })
})

describe('isPairingCode', () => {
  it.each(['ABCD2345', 'ZZZZ9999', '23456789'])('%s 是配对码', (code) => {
    expect(isPairingCode(code)).toBe(true)
  })

  it.each(['ABCD234', 'ABCD23456', 'ABCD2340', 'ABCD2341', 'ABCO2345', 'ABCI2345', 'abcd2345', ''])('%s 不是配对码', (code) => {
    expect(isPairingCode(code)).toBe(false)
  })
})

it('配对码分成两组显示', () => {
  expect(formatPairingCode('ABCD2345')).toBe('ABCD-2345')
})

it('倒计时', () => {
  expect(secondsLeft(10_000, 0)).toBe(10)
  expect(secondsLeft(10_000, 9_001)).toBe(1)
  expect(secondsLeft(10_000, 10_000)).toBe(0)
  expect(secondsLeft(10_000, 20_000)).toBe(0)
  expect(countdownText(300)).toBe('5:00')
  expect(countdownText(65)).toBe('1:05')
  expect(countdownText(0)).toBe('0:00')
})
