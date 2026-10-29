const firstDelayMs = 3000
const maxDelayMs = 30_000

// attempt 从 1 开始计数。
export function reconnectDelay(attempt: number): number {
  return Math.min(firstDelayMs * 2 ** (attempt - 1), maxDelayMs)
}
