import type { SettingSave } from './setting-save'
import { describe, expect, it } from 'vitest'
import { idleSave, settingSave, shownValue } from './setting-save'

const saving: SettingSave<boolean> = { pending: { value: true }, status: 'saving', error: null }

describe('settingSave', () => {
  it('开始保存时控件先显示新值', () => {
    const state = settingSave<boolean>(idleSave, { type: 'start', value: true })
    expect(state).toEqual(saving)
    expect(shownValue(state, false)).toBe(true)
  })

  it('保存中不接受新的修改', () => {
    expect(settingSave(saving, { type: 'start', value: false })).toBe(saving)
  })

  it('保存成功后控件跟随已保存的值', () => {
    const state = settingSave(saving, { type: 'succeeded' })
    expect(state).toEqual({ pending: null, status: 'saved', error: null })
    expect(shownValue(state, true)).toBe(true)
  })

  it('保存失败时控件回到原值，保留原因', () => {
    const state = settingSave(saving, { type: 'failed', error: '暂时不可用' })
    expect(state).toEqual({ pending: null, status: 'failed', error: '暂时不可用' })
    expect(shownValue(state, false)).toBe(false)
  })

  it('再次修改时清掉上次的原因', () => {
    const failed = settingSave(saving, { type: 'failed', error: '暂时不可用' })
    expect(settingSave(failed, { type: 'start', value: true })).toEqual(saving)
  })

  it('“已保存”到时消失，不影响保存中和失败', () => {
    const saved = settingSave(saving, { type: 'succeeded' })
    expect(settingSave(saved, { type: 'expired' })).toEqual(idleSave)
    expect(settingSave(saving, { type: 'expired' })).toBe(saving)
    const failed = settingSave(saving, { type: 'failed', error: '暂时不可用' })
    expect(settingSave(failed, { type: 'expired' })).toBe(failed)
  })

  it('不在保存中时忽略保存的结果', () => {
    expect(settingSave<boolean>(idleSave, { type: 'succeeded' })).toBe(idleSave)
    expect(settingSave<boolean>(idleSave, { type: 'failed', error: '暂时不可用' })).toBe(idleSave)
  })
})
