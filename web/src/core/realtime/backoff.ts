const firstDelayMs = 3000
const maxDelayMs = 30_000
// 服务端重启时会同时断开所有连接，随机错开等待时间，避免客户端同时重连。
const jitter = 0.25

// attempt 从 1 开始计数。
export function reconnectDelay(attempt: number): number {
  const delay = Math.min(firstDelayMs * 2 ** (attempt - 1), maxDelayMs)
  return delay * (1 - jitter + 2 * jitter * Math.random())
}
