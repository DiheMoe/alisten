import type { InlineSaveState } from './inline-save'
import { describe, expect, it } from 'vitest'
import { inlineSave } from './inline-save'

const idle: InlineSaveState = { draft: '大猫', saved: '大猫', status: 'idle', error: null }

describe('inlineSave', () => {
  it('edit 只改 draft', () => {
    expect(inlineSave(idle, { type: 'edit', value: '小猫' })).toEqual({ ...idle, draft: '小猫' })
    const failed: InlineSaveState = { draft: '小猫', saved: '大猫', status: 'error', error: '名字太长' }
    expect(inlineSave(failed, { type: 'edit', value: '小狗' })).toEqual({ ...failed, draft: '小狗' })
  })

  it('submit 时 draft 没变则不进入 saving', () => {
    expect(inlineSave(idle, { type: 'submit' })).toBe(idle)
    const saved: InlineSaveState = { ...idle, status: 'saved' }
    expect(inlineSave(saved, { type: 'submit' })).toBe(saved)
  })

  it('submit 时 draft 改过则进入 saving，并清掉上次的错误', () => {
    expect(inlineSave({ ...idle, draft: '小猫' }, { type: 'submit' })).toEqual({ draft: '小猫', saved: '大猫', status: 'saving', error: null })
    expect(inlineSave({ draft: '小猫', saved: '大猫', status: 'error', error: '名字太长' }, { type: 'submit' })).toEqual({ draft: '小猫', saved: '大猫', status: 'saving', error: null })
  })

  it('改回已保存的值再 submit 时清掉错误', () => {
    expect(inlineSave({ draft: '大猫', saved: '大猫', status: 'error', error: '名字太长' }, { type: 'submit' })).toEqual(idle)
  })

  it('saving 时忽略再次 submit', () => {
    const saving: InlineSaveState = { draft: '小猫', saved: '大猫', status: 'saving', error: null }
    expect(inlineSave(saving, { type: 'submit' })).toBe(saving)
  })

  it('succeeded 后 saved = draft，status = saved', () => {
    const saving: InlineSaveState = { draft: '小猫', saved: '大猫', status: 'saving', error: null }
    expect(inlineSave(saving, { type: 'succeeded', value: '小猫' })).toEqual({ draft: '小猫', saved: '小猫', status: 'saved', error: null })
  })

  it('保存期间又改了 draft 时，succeeded 保留新的 draft', () => {
    const saving: InlineSaveState = { draft: '小猫咪', saved: '大猫', status: 'saving', error: null }
    expect(inlineSave(saving, { type: 'succeeded', value: '小猫' })).toEqual({ draft: '小猫咪', saved: '小猫', status: 'saved', error: null })
  })

  it('failed 后 status = error，保留 draft 和 error', () => {
    const saving: InlineSaveState = { draft: '小猫', saved: '大猫', status: 'saving', error: null }
    expect(inlineSave(saving, { type: 'failed', error: '名字太长' })).toEqual({ draft: '小猫', saved: '大猫', status: 'error', error: '名字太长' })
  })

  it('不在 saving 时忽略 succeeded 和 failed', () => {
    expect(inlineSave(idle, { type: 'succeeded', value: '小猫' })).toBe(idle)
    expect(inlineSave(idle, { type: 'failed', error: '名字太长' })).toBe(idle)
  })

  it('revert 把 draft 恢复为 saved，清掉 error', () => {
    expect(inlineSave({ draft: '小猫', saved: '大猫', status: 'error', error: '名字太长' }, { type: 'revert' })).toEqual(idle)
    expect(inlineSave({ ...idle, draft: '小猫' }, { type: 'revert' })).toEqual(idle)
  })

  it('saving 时忽略 revert', () => {
    const saving: InlineSaveState = { draft: '小猫', saved: '大猫', status: 'saving', error: null }
    expect(inlineSave(saving, { type: 'revert' })).toBe(saving)
  })

  it('external 在 idle 或 saved 时更新 saved 和 draft', () => {
    expect(inlineSave(idle, { type: 'external', value: '老虎' })).toEqual({ ...idle, draft: '老虎', saved: '老虎' })
    expect(inlineSave({ ...idle, status: 'saved' }, { type: 'external', value: '老虎' })).toEqual({ draft: '老虎', saved: '老虎', status: 'saved', error: null })
  })

  it('external 在 saving 或编辑中时只更新 saved', () => {
    expect(inlineSave({ draft: '小猫', saved: '大猫', status: 'saving', error: null }, { type: 'external', value: '老虎' })).toEqual({ draft: '小猫', saved: '老虎', status: 'saving', error: null })
    expect(inlineSave({ ...idle, draft: '小猫' }, { type: 'external', value: '老虎' })).toEqual({ draft: '小猫', saved: '老虎', status: 'idle', error: null })
    expect(inlineSave({ draft: '小猫', saved: '大猫', status: 'error', error: '名字太长' }, { type: 'external', value: '老虎' })).toEqual({ draft: '小猫', saved: '老虎', status: 'error', error: '名字太长' })
  })
})
