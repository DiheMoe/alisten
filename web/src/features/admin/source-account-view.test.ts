import type { CredentialState } from './source-account-view'
import { describe, expect, it } from 'vitest'
import { needsSignIn, sourceAccountView } from './source-account-view'

const cases: { name: string, state: CredentialState, want: ReturnType<typeof sourceAccountView>, attention: boolean }[] = [
  { name: '正在查询', state: undefined, want: { status: '正在查询', tone: 'neutral', detail: '' }, attention: false },
  { name: '查询失败时说明原因', state: new Error('网络不通'), want: { status: '查询失败', tone: 'neutral', detail: '网络不通' }, attention: false },
  { name: '没有保存凭据', state: { set: false, account: null }, want: { status: '未登录', tone: 'danger', detail: '' }, attention: true },
  { name: '保存凭据但没有账号资料', state: { set: true, account: null }, want: { status: '未识别账号', tone: 'danger', detail: '已保存登录信息，但没有识别出账号。' }, attention: true },
  {
    name: '上游已验证的会员账号',
    state: { set: true, account: { id: '123', name: '测试账号', verified: true, vip: true } },
    want: { status: '已登录', tone: 'success', detail: '测试账号 · 会员' },
    attention: false,
  },
  {
    name: '非会员和未知昵称',
    state: { set: true, account: { id: '123', name: null, verified: true, vip: false } },
    want: { status: '已登录', tone: 'success', detail: 'ID 123 · 非会员' },
    attention: false,
  },
  {
    name: '仅从本地凭据识别的账号',
    state: { set: true, account: { id: '456', name: null, verified: false, vip: null } },
    want: { status: '未经核实', tone: 'danger', detail: 'ID 456 · 已保存登录信息，未经 QQ 音乐核实' },
    attention: true,
  },
]

describe('sourceAccountView', () => {
  for (const tc of cases) {
    it(tc.name, () => {
      expect(sourceAccountView(tc.state, 'QQ 音乐')).toEqual(tc.want)
    })
  }
})

// 后台分类栏的“音源”是否显示红点。查询中和查询失败时不知道状态，所以不显示。
describe('needsSignIn', () => {
  for (const tc of cases) {
    it(tc.name, () => {
      expect(needsSignIn(tc.state)).toBe(tc.attention)
    })
  }
})
