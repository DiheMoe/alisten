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

// 两个时间按显示出来的样子比较。显示得一样时，再写一遍更新时间不提供新信息。
export function playlistDates(updatedAtMs: number, createdAtMs: number, now: number): string {
  const created = formatPlaylistDate(createdAtMs, now)
  const updated = formatPlaylistDate(updatedAtMs, now)
  return updated === created ? `创建于${created}` : `更新于${updated}，创建于${created}`
}
