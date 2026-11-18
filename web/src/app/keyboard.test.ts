import { describe, expect, it } from 'vitest'
import { KeyboardDetector } from './keyboard'

describe('输入法是否开着', () => {
  it('文本框有焦点并且页面明显变矮时，输入法开着', () => {
    const detector = new KeyboardDetector()
    expect(detector.observe(400, 800, false)).toBe(false)
    expect(detector.observe(400, 800, true)).toBe(false)
    expect(detector.observe(400, 450, true)).toBe(true)
  })

  it('地址栏收放引起的高度变化不算输入法', () => {
    const detector = new KeyboardDetector()
    detector.observe(400, 800, true)
    expect(detector.observe(400, 744, true)).toBe(false)
  })

  it('收起输入法之后焦点还在文本框里，也不算输入法开着', () => {
    const detector = new KeyboardDetector()
    detector.observe(400, 800, true)
    expect(detector.observe(400, 450, true)).toBe(true)
    expect(detector.observe(400, 800, true)).toBe(false)
  })

  it('没有文本框获得焦点时，页面变矮不算输入法', () => {
    const detector = new KeyboardDetector()
    detector.observe(400, 800, false)
    expect(detector.observe(400, 450, false)).toBe(false)
  })

  it('打开时地址栏展开着，收起之后按更高的高度判断', () => {
    const detector = new KeyboardDetector()
    detector.observe(400, 744, false)
    detector.observe(400, 800, false)
    expect(detector.observe(400, 620, true)).toBe(true)
  })

  it('旋转屏幕之后按新的宽度重新记最大高度', () => {
    const detector = new KeyboardDetector()
    detector.observe(400, 800, false)
    expect(detector.observe(800, 400, true)).toBe(false)
    expect(detector.observe(800, 200, true)).toBe(true)
  })
})
