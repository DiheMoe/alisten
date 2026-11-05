import { describe, expect, it } from 'vitest'
import { panelDirection } from './view-transition'

describe('panelDirection', () => {
  it.each([
    ['queue', 'history', 'forward'],
    ['history', 'queue', 'backward'],
    ['queue', 'chat', 'rise'],
    ['history', 'chat', 'rise'],
    ['chat', 'queue', 'sink'],
    ['chat', 'history', 'sink'],
  ] as const)('%s 到 %s', (from, to, direction) => {
    expect(panelDirection(from, to)).toBe(direction)
  })
})
