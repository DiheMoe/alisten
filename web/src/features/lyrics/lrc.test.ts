import { describe, expect, it } from 'vitest'
import { buildLines, lineIndexAt, parseLrc, parseYrc, withTranslations, wordProgress } from './lrc'

describe('parseLrc', () => {
  it('解析分、秒和不同精度的小数部分', () => {
    expect(parseLrc('[00:12.34]晴天\n[01:02]刮风这天\n[00:01.5]a\n[00:02.123]b')).toEqual([
      { timeMs: 1500, text: 'a' },
      { timeMs: 2123, text: 'b' },
      { timeMs: 12_340, text: '晴天' },
      { timeMs: 62_000, text: '刮风这天' },
    ])
  })

  it('一行有多个时间标签时，每个标签各生成一行', () => {
    expect(parseLrc('[00:05.00][00:01.00] 副歌 ')).toEqual([
      { timeMs: 1000, text: '副歌' },
      { timeMs: 5000, text: '副歌' },
    ])
  })

  it('忽略没有时间标签的行，连续的空白行只保留一行', () => {
    expect(parseLrc('[ar:周杰伦]\n[00:01.00]一\n[00:02.00]\n[00:03.00]  \n[00:04.00]二\r\n[00:05.00]')).toEqual([
      { timeMs: 1000, text: '一' },
      { timeMs: 2000, text: '' },
      { timeMs: 4000, text: '二' },
      { timeMs: 5000, text: '' },
    ])
  })
})

describe('lineIndexAt', () => {
  const lines = [{ timeMs: 1000, text: '一' }, { timeMs: 2000, text: '二' }, { timeMs: 3000, text: '三' }]

  it('返回开始时间不晚于当前位置的最后一行', () => {
    expect(lineIndexAt(lines, 0)).toBe(-1)
    expect(lineIndexAt(lines, 1000)).toBe(0)
    expect(lineIndexAt(lines, 2999)).toBe(1)
    expect(lineIndexAt(lines, 60_000)).toBe(2)
    expect(lineIndexAt([], 1000)).toBe(-1)
  })
})

describe('parseYrc', () => {
  it('解析行和字的绝对时间，行文本由字拼成', () => {
    expect(parseYrc('[1000,900](1000,400,0)故(1400,500,0)事')[0]).toEqual({
      timeMs: 1000,
      text: '故事',
      words: [
        { startMs: 1000, durationMs: 400, text: '故' },
        { startMs: 1400, durationMs: 500, text: '事' },
      ],
    })
  })

  it('保留字里的括号、方括号和空格，只把完整的时间标记当作分隔', () => {
    const [line] = parseYrc('[0,1000](0,500,0)(ooh) (500,250,0)[yeah] (750,250,0)(1,2)')
    expect(line.words?.map(word => word.text)).toEqual(['(ooh) ', '[yeah] ', '(1,2)'])
    expect(line.text).toBe('(ooh) [yeah] (1,2)')
  })

  it('跳过不是 [ 开头的行、没有字的行和全是空白的行，兼容 CRLF', () => {
    const lines = parseYrc('{"t":0,"c":[]}\r\n\r\n[0,500]\r\n[500,500](500,500,0)  \r\n注[700,100](700,100,0)释\r\n[1000,500](1000,500,0)词\r\n')
    expect(lines.map(line => line.text)).toEqual(['词', ''])
    expect(lines[0].words).toEqual([{ startMs: 1000, durationMs: 500, text: '词' }])
  })

  it('按行起始时间排序', () => {
    const lines = parseYrc('[2000,500](2000,500,0)二\n[1000,500](1000,500,0)一')
    expect(lines.filter(line => line.text).map(line => line.text)).toEqual(['一', '二'])
  })

  it('两行之间有间奏时在上一行结束处补空白行，换气的短停顿不补', () => {
    const lines = parseYrc('[0,1000](0,1000,0)一\n[1500,1000](1500,1000,0)二\n[20000,1000](20000,1000,0)三')
    expect(lines.map(({ timeMs, text }) => [timeMs, text])).toEqual([
      [0, '一'],
      [1500, '二'],
      [2500, ''],
      [20_000, '三'],
      [21_000, ''],
    ])
  })
})

describe('wordProgress', () => {
  const word = { startMs: 1000, durationMs: 400, text: '故' }

  it('唱到这个字之前是 0，唱完之后是 1，中间按时间线性增长', () => {
    expect(wordProgress(word, 999)).toBe(0)
    expect(wordProgress(word, 1100)).toBe(0.25)
    expect(wordProgress(word, 1400)).toBe(1)
    expect(wordProgress(word, 5000)).toBe(1)
  })

  it('时长为 0 的字唱到时直接点亮', () => {
    const instant = { startMs: 1000, durationMs: 0, text: '故' }
    expect(wordProgress(instant, 999)).toBe(0)
    expect(wordProgress(instant, 1000)).toBe(0)
    expect(wordProgress(instant, 1001)).toBe(1)
  })
})

describe('withTranslations', () => {
  const line = (timeMs: number, text: string) => ({ timeMs, text })

  it('翻译配到时间最近的一行，容忍取整造成的偏差', () => {
    expect(withTranslations([line(1000, '一'), line(3000, '二')], [line(991, 'one'), line(3009, 'two')])).toEqual([
      { timeMs: 1000, text: '一', translation: 'one' },
      { timeMs: 3000, text: '二', translation: 'two' },
    ])
  })

  it('翻译离后一行更近时配到后一行', () => {
    const paired = withTranslations([line(1000, '一'), line(1400, '二')], [line(1390, 'two')])
    expect(paired.map(({ translation }) => translation)).toEqual([undefined, 'two'])
  })

  it('偏差超过 500ms 时视为这一行没有翻译', () => {
    expect(withTranslations([line(1000, '一')], [line(1500, 'one')])[0].translation).toBe('one')
    expect(withTranslations([line(1000, '一')], [line(1501, 'one')])[0].translation).toBeUndefined()
  })

  it('没有翻译的行不借用相邻行的翻译', () => {
    const paired = withTranslations([line(1000, '一'), line(1300, '二')], [line(1000, 'one')])
    expect(paired.map(({ translation }) => translation)).toEqual(['one', undefined])
  })

  it('同一行有多条候选翻译时取最近的一条', () => {
    expect(withTranslations([line(1000, '一')], [line(800, 'far'), line(990, 'near'), line(1200, 'far too')])[0].translation).toBe('near')
  })

  it('空白行和空翻译不参与配对', () => {
    const paired = withTranslations([line(1000, '一'), line(1100, ''), line(5000, '二')], [line(1100, 'one'), line(5000, '')])
    expect(paired).toEqual([
      { timeMs: 1000, text: '一', translation: 'one' },
      { timeMs: 1100, text: '' },
      { timeMs: 5000, text: '二' },
    ])
  })

  it('没有歌词行时不报错', () => {
    expect(withTranslations([], [line(1000, 'one')])).toEqual([])
  })
})

describe('buildLines', () => {
  it('有逐字歌词时用逐字歌词的行，翻译配到这些行上', () => {
    const lines = buildLines('[00:01.00]行级歌词', '[1000,500](1000,500,0)逐(1250,250,0)字', '[ti:标题]\n[00:01.00]Word by word')
    expect(lines[0]).toMatchObject({ timeMs: 1000, text: '逐字', translation: 'Word by word' })
    expect(lines[0].words).toHaveLength(2)
  })

  it('没有逐字歌词时保持行级歌词，不带逐字数据', () => {
    expect(buildLines('[00:01.00]行级歌词')).toEqual([{ timeMs: 1000, text: '行级歌词' }])
  })

  it('逐字歌词解析不出内容时退回行级歌词', () => {
    expect(buildLines('[00:01.00]行级歌词', '{"t":0}')).toEqual([{ timeMs: 1000, text: '行级歌词' }])
  })
})
