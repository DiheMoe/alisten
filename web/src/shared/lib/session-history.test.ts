import { afterEach, describe, expect, it, vi } from 'vitest'
import { afterMarker, afterPop, afterPush, canGoBack, canGoForward } from './session-history'

describe('历史记录的序号', () => {
  it('第一条记录不能后退，写入新记录之后可以后退、不能前进', () => {
    const first = { index: 0, last: 0 }
    expect(canGoBack(first)).toBe(false)
    const second = afterPush(first)
    expect(canGoBack(second)).toBe(true)
    expect(canGoForward(second)).toBe(false)
  })

  it('后退之后可以前进，再写入新记录就丢掉了前面的记录', () => {
    const back = afterPop({ index: 3, last: 3 }, 1)
    expect(back).toEqual({ index: 1, last: 3 })
    expect(canGoForward(back)).toBe(true)
    expect(afterPush(back)).toEqual({ index: 2, last: 2 })
  })

  it('刷新之后不知道前面的记录，前进到了才知道', () => {
    expect(afterPop({ index: 1, last: 1 }, 2)).toEqual({ index: 2, last: 2 })
  })

  it('打开弹窗的标记记录不占序号，同样丢掉前面的记录', () => {
    const marked = afterMarker({ index: 1, last: 3 })
    expect(marked).toEqual({ index: 1, last: 1 })
    expect(canGoForward(marked)).toBe(false)
  })
})

interface HistoryEntry {
  state: unknown
  url: string
}

// 和浏览器一样，back() 要到之后的任务才完成后退。settle() 模拟后退完成时派发的 popstate。
class FakeHistory {
  entries: HistoryEntry[]
  at = 0
  traversals: number[] = []
  failure: Error | null = null
  readonly events = new EventTarget()

  constructor(state: unknown) {
    this.entries = [{ state, url: '/start' }]
  }

  get current(): HistoryEntry {
    return this.entries[this.at] as HistoryEntry
  }

  get state(): unknown {
    return this.current.state
  }

  get urls(): string[] {
    return this.entries.map(entry => entry.url)
  }

  pushState(state: unknown, _title: string, url: string): void {
    if (this.failure)
      throw this.failure
    this.entries = [...this.entries.slice(0, this.at + 1), { state, url }]
    this.at += 1
  }

  replaceState(state: unknown, _title: string, url?: string): void {
    if (this.failure)
      throw this.failure
    this.entries[this.at] = { state, url: url ?? this.current.url }
  }

  back(): void {
    this.traversals.push(-1)
  }

  settle(): void {
    const delta = this.traversals.shift()
    if (delta === undefined)
      throw new Error('没有等待中的后退')
    this.pop(delta)
  }

  pop(delta: number): void {
    this.at += delta
    this.events.dispatchEvent(Object.assign(new Event('popstate'), { state: this.state }))
  }

  press(delta = -1): void {
    this.pop(delta)
  }
}

async function open(state: unknown = null) {
  vi.resetModules()
  const history = new FakeHistory(state)
  vi.stubGlobal('window', {
    history,
    location: {
      get href() {
        return history.current.url
      },
      get pathname() {
        return new URL(history.current.url, 'https://alisten.test').pathname
      },
      get search() {
        return new URL(history.current.url, 'https://alisten.test').search
      },
    },
    addEventListener: (type: string, listener: EventListener) => history.events.addEventListener(type, listener),
  })
  const module = await import('./session-history')
  const log: string[] = []
  const firstOpen = module.startHistory(() => log.push('notify'))
  return { ...module, history, log, firstOpen }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('打开页面', () => {
  it('第一次打开时给当前记录写上序号', async () => {
    const { firstOpen, history, currentEntry } = await open()
    expect(firstOpen).toBe(true)
    expect(history.state).toEqual({ index: 0 })
    expect(currentEntry().position).toEqual({ index: 0, last: 0 })
  })

  it('刷新时沿用记录里的序号和标题', async () => {
    const { firstOpen, history, currentEntry } = await open({ index: 3, title: '房间' })
    expect(firstOpen).toBe(false)
    expect(history.state).toEqual({ index: 3, title: '房间' })
    expect(currentEntry()).toEqual({ position: { index: 3, last: 3 }, title: '房间', from: undefined })
  })

  it('刷新时弹窗已经关闭，标记记录改作页面的记录', async () => {
    const { firstOpen, history } = await open({ index: 2, title: '房间', modal: true })
    expect(firstOpen).toBe(false)
    expect(history.state).toEqual({ index: 2, title: '房间' })
  })

  it.each([
    ['字符串', 'other'],
    ['没有序号', {}],
    ['负数序号', { index: -1 }],
    ['小数序号', { index: 1.5 }],
  ])('别的脚本写入的状态（%s）当作第一次打开', async (_, state) => {
    const { firstOpen, history } = await open(state)
    expect(firstOpen).toBe(true)
    expect(history.state).toEqual({ index: 0 })
  })
})

describe('写入记录', () => {
  it('新记录的序号加一，后退和前进由 popstate 同步', async () => {
    const { writeEntry, currentEntry, history, log } = await open()
    writeEntry('/a', false, '甲')
    writeEntry('/b', false)
    expect(history.urls).toEqual(['/start', '/a', '/b'])
    expect(currentEntry()).toEqual({ position: { index: 2, last: 2 }, title: undefined, from: '/a' })

    history.press()
    expect(currentEntry()).toEqual({ position: { index: 1, last: 2 }, title: '甲', from: '/start' })
    expect(log).toEqual(['notify'])
  })

  it('替换时序号不变，不给标题就沿用原来的', async () => {
    const { writeEntry, currentEntry, history } = await open({ index: 1, title: '房间' })
    writeEntry('/a', true)
    expect(history.state).toEqual({ index: 1, title: '房间' })
    writeEntry('/b', true, '搜索')
    expect(history.urls).toEqual(['/b'])
    expect(currentEntry()).toEqual({ position: { index: 1, last: 1 }, title: '搜索', from: undefined })
  })

  it('浏览器限制调用频率时地址暂时落后，序号不变，页面照常', async () => {
    const { writeEntry, currentEntry, history } = await open()
    history.failure = new DOMException('Too many calls', 'SecurityError')
    expect(() => writeEntry('/a', false)).not.toThrow()
    expect(() => writeEntry('/b', true, '甲')).not.toThrow()
    expect(currentEntry()).toEqual({ position: { index: 0, last: 0 }, title: undefined, from: undefined })

    history.failure = null
    writeEntry('/c', false)
    expect(currentEntry().position).toEqual({ index: 1, last: 1 })
  })

  it('后退到别的脚本写入的记录时，序号保持不变', async () => {
    const { writeEntry, currentEntry, history, log } = await open()
    writeEntry('/a', false)
    writeEntry('/b', false)
    history.entries[1] = { state: 'other', url: '/a' }
    history.press()
    expect(currentEntry().position).toEqual({ index: 2, last: 2 })
    expect(log).toEqual(['notify'])
  })

  it('新记录记下写入时的地址，也就是它前面那条记录的地址；替换时沿用，刷新之后还在', async () => {
    const { writeEntry, currentEntry, history } = await open()
    writeEntry('/settings', true)
    writeEntry('/settings/appearance', false)
    expect(currentEntry().from).toBe('/settings')
    writeEntry('/admin/media?q=a', false)
    writeEntry('/admin/media?q=ab', true)
    expect(currentEntry().from).toBe('/settings/appearance')
    expect(history.state).toEqual({ index: 2, from: '/settings/appearance' })

    history.press()
    expect(currentEntry().from).toBe('/settings')
  })

  it('刷新时沿用记录里的来处，别的脚本写入的来处不用', async () => {
    expect((await open({ index: 2, from: '/settings' })).currentEntry().from).toBe('/settings')
    const other = await open({ index: 2, from: 3 })
    expect(other.currentEntry().from).toBeUndefined()
    expect(other.history.state).toEqual({ index: 2, from: 3 })
    expect((await open({ index: 2, from: '/settings', modal: true })).history.state).toEqual({ index: 2, from: '/settings' })
  })

  it('其他错误照常抛出', async () => {
    const { writeEntry, history } = await open()
    history.failure = new DOMException('bad', 'DataCloneError')
    expect(() => writeEntry('/a', false)).toThrow('bad')
  })
})

describe('弹窗', () => {
  it('整个弹窗栈只占一条记录，标记不占序号', async () => {
    const { registerModalHistory, currentEntry, history } = await open()
    registerModalHistory(() => {})
    registerModalHistory(() => {})
    expect(history.entries).toHaveLength(2)
    expect(history.state).toEqual({ index: 0, modal: true })
    expect(currentEntry().position).toEqual({ index: 0, last: 0 })
  })

  it('返回键只关最上层的弹窗，下面还有弹窗时重新压入标记', async () => {
    const { registerModalHistory, history, log } = await open()
    registerModalHistory(() => log.push('close 1'))
    registerModalHistory(() => log.push('close 2'))
    history.press()
    expect(log).toEqual(['notify', 'close 2'])
    expect(history.state).toEqual({ index: 0, modal: true })

    history.press()
    expect(log).toEqual(['notify', 'close 2', 'notify', 'close 1'])
    expect(history.state).toEqual({ index: 0 })
  })

  it('代码关闭弹窗时退掉标记，关闭之前再调用也不重复后退', async () => {
    const { registerModalHistory, history, log } = await open()
    const close = registerModalHistory(() => log.push('close'))
    close()
    close()
    expect(history.traversals).toEqual([-1])
    history.settle()
    expect(history.state).toEqual({ index: 0 })
    expect(log).toEqual(['notify'])
  })

  it('重复关闭同一个弹窗时不影响其他弹窗', async () => {
    const { registerModalHistory, history, log } = await open()
    const close = registerModalHistory(() => log.push('close 1'))
    registerModalHistory(() => log.push('close 2'))
    close()
    close()
    history.press()
    expect(log).toEqual(['notify', 'close 2'])
  })

  it('正在退掉标记时关闭弹窗，不再后退一次', async () => {
    const { registerModalHistory, writeEntry, history } = await open()
    const close = registerModalHistory(() => {})
    writeEntry('/filtered', true)
    close()
    expect(history.traversals).toEqual([-1])
  })

  it('后退之后打开弹窗，前面的记录就没有了', async () => {
    const { registerModalHistory, writeEntry, currentEntry, history } = await open()
    writeEntry('/a', false)
    history.press()
    expect(currentEntry().position).toEqual({ index: 0, last: 1 })
    registerModalHistory(() => {})
    expect(currentEntry().position).toEqual({ index: 0, last: 0 })
  })

  it('下面还有弹窗时，关闭上层的弹窗不后退', async () => {
    const { registerModalHistory, history } = await open()
    registerModalHistory(() => {})
    const close = registerModalHistory(() => {})
    close()
    expect(history.traversals).toEqual([])
  })

  it('退掉标记期间的写入排队，落在页面的记录上', async () => {
    const { registerModalHistory, writeEntry, currentEntry, history } = await open()
    const close = registerModalHistory(() => {})
    close()
    writeEntry('/next', false, '下一页')
    expect(history.urls).toEqual(['/start', '/start'])

    history.settle()
    expect(history.urls).toEqual(['/start', '/next'])
    expect(currentEntry()).toEqual({ position: { index: 1, last: 1 }, title: '下一页', from: '/start' })
  })

  it('弹窗开着时替换地址：先退到页面的记录再替换，然后压回标记', async () => {
    const { registerModalHistory, writeEntry, history, log } = await open()
    registerModalHistory(() => log.push('close'))
    writeEntry('/filtered', true)
    writeEntry('/filtered-more', true)
    expect(history.traversals).toEqual([-1])

    history.settle()
    expect(history.urls).toEqual(['/filtered-more', '/filtered-more'])
    expect(history.entries.map(entry => entry.state)).toEqual([{ index: 0 }, { index: 0, modal: true }])
    expect(log).toEqual(['notify'])
  })

  it('在弹窗里换页面时，新页面的记录顶替标记', async () => {
    const { registerModalHistory, writeEntry, currentEntry, history } = await open()
    registerModalHistory(() => {})
    writeEntry('/room', false)
    expect(history.urls).toEqual(['/start', '/room'])
    expect(history.state).toEqual({ index: 1, from: '/start' })
    expect(currentEntry().position).toEqual({ index: 1, last: 1 })
  })

  it('前进到已经关闭的弹窗留下的标记时退回页面', async () => {
    const { registerModalHistory, history, log } = await open()
    registerModalHistory(() => {})()
    history.settle()
    log.length = 0

    history.press(1)
    expect(history.traversals).toEqual([-1])
    expect(log).toEqual([])
    history.settle()
    expect(history.state).toEqual({ index: 0 })
    expect(log).toEqual(['notify'])
  })

  it('写不进标记时弹窗照常打开，返回键关掉它的同时后退一页', async () => {
    const { registerModalHistory, writeEntry, history, log } = await open()
    writeEntry('/a', false)
    history.failure = new DOMException('Too many calls', 'SecurityError')
    registerModalHistory(() => log.push('close'))
    history.failure = null
    expect(history.entries).toHaveLength(2)

    history.press()
    expect(history.urls[history.at]).toBe('/start')
    expect(log).toEqual(['notify', 'close'])
  })

  it('页面先跟上记录再关弹窗：关弹窗时可能接着导航', async () => {
    const { registerModalHistory, writeEntry, history, log } = await open()
    registerModalHistory(() => {
      log.push('close')
      writeEntry('/after-close', false)
    })
    history.press()
    expect(log).toEqual(['notify', 'close'])
    expect(history.urls).toEqual(['/start', '/after-close'])
  })
})

describe('后退一页', () => {
  it('没有弹窗时直接后退', async () => {
    const { goBack, writeEntry, history, log } = await open()
    writeEntry('/a', false)
    goBack()
    expect(history.traversals).toEqual([-1])
    history.settle()
    expect(log).toEqual(['notify'])
  })

  it('标记压在页面上时只退到页面的记录，已经在退时不再后退', async () => {
    const { goBack, registerModalHistory, history } = await open()
    registerModalHistory(() => {})
    goBack()
    goBack()
    expect(history.traversals).toEqual([-1])
  })
})
