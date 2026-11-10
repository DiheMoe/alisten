import { describe, expect, it } from 'vitest'
import { notificationHint, notificationRefusal, sectionSummaries } from './settings-labels'

const defaults = { theme: 'dark', level: 'medium', volume: 75, muted: false, recordListens: true } as const

describe('sectionSummaries', () => {
  it('手机端分类列表显示各分类的当前值', () => {
    expect(sectionSummaries(defaults)).toEqual({ appearance: '深色', playback: '75%', performance: '平衡', privacy: '开' })
    expect(sectionSummaries({ ...defaults, theme: 'system', level: 'off', recordListens: false })).toEqual({ appearance: '跟随系统', playback: '75%', performance: '极简', privacy: '关' })
  })

  it('静音时显示静音', () => {
    expect(sectionSummaries({ ...defaults, muted: true }).playback).toBe('静音')
  })

  it('还没读到记录播放历史的设置时不显示', () => {
    expect(sectionSummaries({ ...defaults, recordListens: null }).privacy).toBeUndefined()
  })
})

describe('notificationHint', () => {
  it('还没授权时提供授权按钮', () => {
    expect(notificationHint('default')).toEqual({ description: '需要浏览器授权，现在还没授权', authorize: true })
  })

  it('已经授权时不提供授权按钮', () => {
    expect(notificationHint('granted').authorize).toBe(false)
  })

  it('浏览器拒绝之后不能再申请，提示去网站设置里允许', () => {
    expect(notificationHint('denied')).toEqual({ description: '浏览器不允许显示通知，要在浏览器的网站设置里允许', authorize: false })
  })
})

describe('notificationRefusal', () => {
  it('被拒绝之后说明里已经写了去哪里允许，只说结果', () => {
    expect(notificationRefusal('denied')).toBe('浏览器拒绝了显示通知。')
  })

  it('提示被关掉或者被浏览器收起时，告诉用户去浏览器的网站设置里允许', () => {
    expect(notificationRefusal('default')).toBe('浏览器没有授权，可以在浏览器的网站设置里允许通知。')
  })
})
