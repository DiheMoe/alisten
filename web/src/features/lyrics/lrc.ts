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
  dots?: LyricWord[]
}

const timeTag = /\[(\d{1,2}):(\d{2})(?:\.(\d{1,3}))?\]/g
const yrcLineTag = /^\[(\d+),(\d+)\]/
const yrcWordTag = /\((\d+),(\d+),\d+\)/g
// 逐字歌词没有空白行，要靠行尾和下一行的间隔判断间奏。间隔太短就算作换气：清除高亮再点亮会闪烁。
const interludeGapMs = 3000
const maxTranslationGapMs = 500
const minInterludeMs = 7000
const interludeDots = 3
// 字亮起和声音同时发生时，看起来反而慢半拍。
export const lyricLeadMs = 100

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
    const words: LyricWord[] = []
    for (const [i, tag] of tags.entries()) {
      const text = row.slice(tag.index + tag[0].length, tags[i + 1]?.index)
      if (text.trim())
        words.push({ startMs: Number(tag[1]), durationMs: Number(tag[2]), text })
      // 字独立绘制：只含空格的字单独成字时，它两侧的空格不会合并，词间距会变成两倍。
      else if (words.length)
        words[words.length - 1].text += text
    }
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

// 唱得久的短词才强调：短音节一闪而过看不出来，长词放大会挤到相邻的字。
export function wordEmphasis({ durationMs, text }: LyricWord): number {
  const { length } = text.trim()
  return durationMs > 1000 && length > 0 && length < 8 ? Math.min(durationMs, 2000) / 1000 - 1 : 0
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

// 长间奏用点倒计时：每个点是一个不带文字的字，绘制时和逐字歌词走同一条路径。
export function withInterludes(lines: LyricLine[]): LyricLine[] {
  const first = lines.find(line => line.text)
  const withIntro = first && lines[0].timeMs > 0 && first.timeMs >= minInterludeMs ? [{ timeMs: 0, text: '' }, ...lines] : lines
  return withIntro.map((line, i) => {
    const next = withIntro[i + 1]
    if (line.text || !next?.text || next.timeMs - line.timeMs < minInterludeMs)
      return line
    const durationMs = (next.timeMs - line.timeMs) / interludeDots
    return { ...line, dots: Array.from({ length: interludeDots }, (_, dot) => ({ startMs: line.timeMs + dot * durationMs, durationMs, text: '' })) }
  })
}

export function buildLines(lyric: string, words = '', translation = ''): LyricLine[] {
  const wordLines = parseYrc(words)
  return withInterludes(withTranslations(wordLines.length ? wordLines : parseLrc(lyric), parseLrc(translation)))
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
