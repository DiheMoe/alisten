export interface LyricLine {
  timeMs: number
  text: string
}

const timeTag = /\[(\d{1,2}):(\d{2})(?:\.(\d{1,3}))?\]/g

export function parseLrc(lrc: string): LyricLine[] {
  const lines: LyricLine[] = []
  for (const row of lrc.split('\n')) {
    const tags = [...row.matchAll(timeTag)]
    if (!tags.length)
      continue
    const text = row.replace(timeTag, '').trim()
    for (const [, minutes, seconds, fraction = ''] of tags)
      lines.push({ timeMs: Number(minutes) * 60_000 + Number(seconds) * 1000 + Number(fraction.padEnd(3, '0')), text })
  }
  lines.sort((a, b) => a.timeMs - b.timeMs)
  // 空白行只用于同步，连续的空白行合并为一行。
  return lines.filter((line, i) => line.text || i === 0 || lines[i - 1].text)
}

export function lineIndexAt(lines: LyricLine[], positionMs: number): number {
  let low = 0
  let high = lines.length - 1
  let found = -1
  while (low <= high) {
    const mid = (low + high) >> 1
    if (lines[mid].timeMs <= positionMs) {
      found = mid
      low = mid + 1
    }
    else {
      high = mid - 1
    }
  }
  return found
}
