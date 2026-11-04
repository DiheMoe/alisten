export interface LyricWord {
  startMs: number
  durationMs: number
  text: string
}

export interface LyricLine {
  timeMs: number
  text: string
  words?: LyricWord[]
  translation?: string
}

const timeTag = /\[(\d{1,2}):(\d{2})(?:\.(\d{1,3}))?\]/g
const yrcLineTag = /^\[(\d+),(\d+)\]/
const yrcWordTag = /\((\d+),(\d+),\d+\)/g
// 逐字歌词没有空白行，要靠行尾和下一行的间隔判断间奏。间隔太短就算作换气：清除高亮再点亮会闪烁。
const interludeGapMs = 3000
const maxTranslationGapMs = 500

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

export function parseYrc(yrc: string): LyricLine[] {
  const lines: (LyricLine & { endMs: number })[] = []
  for (const row of yrc.split(/\r?\n/)) {
    const head = yrcLineTag.exec(row)
    if (!head)
      continue
    const tags = [...row.matchAll(yrcWordTag)]
    const words = tags.map((tag, i) => ({
      startMs: Number(tag[1]),
      durationMs: Number(tag[2]),
      text: row.slice(tag.index + tag[0].length, tags[i + 1]?.index),
    }))
    const text = words.map(word => word.text).join('').trim()
    const timeMs = Number(head[1])
    if (text)
      lines.push({ timeMs, text, words, endMs: timeMs + Number(head[2]) })
  }
  lines.sort((a, b) => a.timeMs - b.timeMs)
  return lines.flatMap(({ endMs, ...line }, i) => {
    const next = lines[i + 1]
    return !next || next.timeMs - endMs >= interludeGapMs ? [line, { timeMs: endMs, text: '' }] : [line]
  })
}

export function wordProgress({ startMs, durationMs }: LyricWord, positionMs: number): number {
  return Math.min(Math.max((positionMs - startMs) / Math.max(durationMs, 1), 0), 1)
}

// 由翻译找行，而不是由行找翻译：否则没有翻译的行会借用相邻行的翻译。
export function withTranslations(lines: LyricLine[], translations: LyricLine[]): LyricLine[] {
  const sung = lines.filter(line => line.text)
  const gap = (a: LyricLine, b: LyricLine) => Math.abs(a.timeMs - b.timeMs)
  const nearest = new Map<LyricLine, LyricLine>()
  for (const translation of translations) {
    const index = lineIndexAt(sung, translation.timeMs)
    const [line] = sung.slice(Math.max(index, 0), index + 2).sort((a, b) => gap(a, translation) - gap(b, translation))
    if (!line || !translation.text || gap(line, translation) > maxTranslationGapMs)
      continue
    const current = nearest.get(line)
    if (!current || gap(line, translation) < gap(line, current))
      nearest.set(line, translation)
  }
  return lines.map((line) => {
    const translation = nearest.get(line)
    return translation ? { ...line, translation: translation.text } : line
  })
}

export function buildLines(lyric: string, words = '', translation = ''): LyricLine[] {
  const wordLines = parseYrc(words)
  return withTranslations(wordLines.length ? wordLines : parseLrc(lyric), parseLrc(translation))
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
