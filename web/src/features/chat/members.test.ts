import type { Member } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { groupSessions } from './members'

const alice: Member = { id: 'a', name: 'alice', avatarHash: '' }
const bob: Member = { id: 'b', name: 'bob', avatarHash: '' }

describe('groupSessions', () => {
  it('同一成员的多个会话合成一项，记下设备数，按第一次出现的顺序排列', () => {
    expect(groupSessions([alice, bob, alice, alice])).toEqual([{ member: alice, devices: 3 }, { member: bob, devices: 1 }])
  })

  it('没有会话时为空', () => {
    expect(groupSessions([])).toEqual([])
  })
})
