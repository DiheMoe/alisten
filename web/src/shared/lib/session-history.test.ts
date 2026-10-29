import { describe, expect, it } from 'vitest'
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
