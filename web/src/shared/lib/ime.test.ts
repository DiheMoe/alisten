import { describe, expect, it } from 'vitest'
import { isImeKey } from './ime'

describe('isImeKey', () => {
  it('组字期间的按键交给输入法', () => {
    expect(isImeKey({ isComposing: true, keyCode: 13 })).toBe(true)
  })

  it('在 Safari 里用回车确认候选词时，组字已经先结束，只剩 keyCode 229 能认出来', () => {
    expect(isImeKey({ isComposing: false, keyCode: 229 })).toBe(true)
  })

  it('平时的回车和 Esc 交给输入框', () => {
    expect(isImeKey({ isComposing: false, keyCode: 13 })).toBe(false)
    expect(isImeKey({ isComposing: false, keyCode: 27 })).toBe(false)
  })
})
