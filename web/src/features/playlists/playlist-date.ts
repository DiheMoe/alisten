const dayMs = 24 * 60 * 60 * 1000

function dayStart(date: Date): number {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

export function formatPlaylistDate(ms: number, now: number): string {
  const date = new Date(ms)
  const today = new Date(now)
  // 夏令时切换的那天不是 24 小时，按天取整。
  const days = Math.round((dayStart(today) - dayStart(date)) / dayMs)
  if (days === 0 || days === 1)
    return `${days === 0 ? '今天' : '昨天'} ${pad(date.getHours())}:${pad(date.getMinutes())}`
  const day = `${date.getMonth() + 1}月${date.getDate()}日`
  return date.getFullYear() === today.getFullYear() ? day : `${date.getFullYear()}年${day}`
}
