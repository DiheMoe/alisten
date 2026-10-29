import type { ErrorCode } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { ApiError, IdentityRenewedError } from '@/core/api'
import { RequestError } from '@/core/realtime'
import { errorMessage, errorText } from './error-message'

describe('errorText', () => {
  const cases: { name: string, code: ErrorCode, message: string, want: string }[] = [
    { name: '字段路径换成通用说法', code: 'INVALID_ARGUMENT', message: 'name：长度不能少于 1 个字符', want: '提交的内容不符合要求，请检查后再试。' },
    { name: '协议版本不一致', code: 'CLIENT_OUTDATED', message: '前端与服务端的协议版本不一致，请刷新页面', want: '应用已经更新，稍后会自动刷新页面。' },
    { name: '房间密码错误照旧显示', code: 'UNAUTHORIZED', message: '房间密码错误', want: '房间密码错误' },
    { name: '重复点歌照旧显示', code: 'CONFLICT', message: '这首歌已经在播放列表里了。', want: '这首歌已经在播放列表里了。' },
    { name: '播放列表已满照旧显示', code: 'QUEUE_FULL', message: '临时房间的播放列表最多 10 首歌，等播放几首之后再点。', want: '临时房间的播放列表最多 10 首歌，等播放几首之后再点。' },
    { name: '没有在放的歌照旧显示', code: 'NOTHING_PLAYING', message: '现在没有在放的歌。', want: '现在没有在放的歌。' },
    { name: '音乐平台出错照旧显示', code: 'PROVIDER_ERROR', message: '音乐平台没有正常响应，请稍后再试。', want: '音乐平台没有正常响应，请稍后再试。' },
    { name: '房间暂时不可用照旧显示', code: 'UNAVAILABLE', message: '房间暂时不可用，请稍后再试。', want: '房间暂时不可用，请稍后再试。' },
    { name: '服务器出错照旧显示', code: 'INTERNAL', message: '服务器出错了，请稍后再试。', want: '服务器出错了，请稍后再试。' },
  ]

  for (const tc of cases) {
    it(tc.name, () => {
      expect(errorText(tc.code, tc.message)).toBe(tc.want)
    })
  }

  it('操作给出的说法优先', () => {
    expect(errorText('INVALID_ARGUMENT', 'keyword：长度不能超过 500 个字符', { INVALID_ARGUMENT: '关键词太长了' })).toBe('关键词太长了')
    expect(errorText('CONFLICT', '这首歌已经在播放列表里了。', { INVALID_ARGUMENT: '关键词太长了' })).toBe('这首歌已经在播放列表里了。')
  })

  it('操作可以照原样显示服务端的 message', () => {
    expect(errorText('INVALID_ARGUMENT', '不是能识别的 cookies', { INVALID_ARGUMENT: message => message })).toBe('不是能识别的 cookies')
  })
})

describe('errorMessage', () => {
  it('按错误码转换服务端的错误', () => {
    expect(errorMessage(new ApiError('cookies：长度不能少于 1 个字符', 'INVALID_ARGUMENT', 400))).toBe('提交的内容不符合要求，请检查后再试。')
    expect(errorMessage(new RequestError('前端与服务端的协议版本不一致，请刷新页面', 'CLIENT_OUTDATED'))).toBe('应用已经更新，稍后会自动刷新页面。')
    expect(errorMessage(new RequestError('你已经投过票了。', 'CONFLICT'))).toBe('你已经投过票了。')
  })

  it('前端构造的错误照旧显示', () => {
    expect(errorMessage(new ApiError('无法连接服务器，请检查网络后再试。', null, 0))).toBe('无法连接服务器，请检查网络后再试。')
    expect(errorMessage(new RequestError('与房间的连接断开了，连上之后再试一次。', null))).toBe('与房间的连接断开了，连上之后再试一次。')
    expect(errorMessage(new IdentityRenewedError())).toBe(new IdentityRenewedError().message)
  })

  it('浏览器和程序缺陷的错误不显示原文', () => {
    expect(errorMessage(new TypeError('Cannot read properties of undefined'))).toBe('出错了，请稍后再试。')
    expect(errorMessage('boom')).toBe('出错了，请稍后再试。')
  })

  it('操作给出的说法只替换对应的错误码', () => {
    const overrides = { INVALID_ARGUMENT: '昵称不能为空' }
    expect(errorMessage(new ApiError('name：长度不能少于 1 个字符', 'INVALID_ARGUMENT', 400), overrides)).toBe('昵称不能为空')
    expect(errorMessage(new ApiError('服务器出错了，请稍后再试。', 'INTERNAL', 500), overrides)).toBe('服务器出错了，请稍后再试。')
  })
})
