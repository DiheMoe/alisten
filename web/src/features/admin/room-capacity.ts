import { RoomCapacityLimits } from '@/core/realtime'

export const capacityRangeError = `最多人数要在 ${RoomCapacityLimits.minimum} 到 ${RoomCapacityLimits.maximum} 之间。`

// 不是范围内的整数时返回 null。
export function parseCapacity(draft: string): number | null {
  const capacity = Number(draft)
  return Number.isInteger(capacity) && capacity >= RoomCapacityLimits.minimum && capacity <= RoomCapacityLimits.maximum ? capacity : null
}
