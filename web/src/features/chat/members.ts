import type { Member } from '@/core/realtime'

export interface OnlineMember {
  member: Member
  devices: number
}

// 在线列表按人展示。同一成员开着多个标签页或客户端时，每个连接是一个会话，这些会话合成一项，并记下有几个。
export function groupSessions(sessions: readonly Member[]): OnlineMember[] {
  const people = new Map<string, OnlineMember>()
  for (const member of sessions) {
    const seen = people.get(member.id)
    if (seen)
      seen.devices += 1
    else
      people.set(member.id, { member, devices: 1 })
  }
  return [...people.values()]
}
