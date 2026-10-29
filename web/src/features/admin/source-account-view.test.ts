import type { AdminGetNeteaseCredentialResponse, AdminGetQQCredentialResponse } from '@/core/api'
import { describe, expect, it } from 'vitest'
import { sourceAccountView } from './source-account-view'

type State = AdminGetNeteaseCredentialResponse | AdminGetQQCredentialResponse | null | undefined

const cases: { name: string, state: State, want: ReturnType<typeof sourceAccountView> }[] = [
  { name: '正在查询', state: undefined, want: { status: '正在查询…', details: [] } },
  { name: '查询失败', state: null, want: { status: '查询失败', details: [] } },
  { name: '没有保存凭据', state: { set: false, account: null }, want: { status: '未登录', details: [] } },
  { name: '保存凭据但没有账号资料', state: { set: true, account: null }, want: { status: '已保存登录信息，未识别到账号', details: [] } },
  {
    name: '上游已验证的会员账号',
    state: { set: true, account: { id: '123', name: '测试账号', verified: true, vip: true } },
    want: { status: '已登录', details: ['昵称：测试账号', '账号 ID：123', '会员状态：会员'] },
  },
  {
    name: '非会员和未知昵称',
    state: { set: true, account: { id: '123', name: null, verified: true, vip: false } },
    want: { status: '已登录', details: ['账号 ID：123', '会员状态：非会员'] },
  },
  {
    name: '仅从本地凭据识别的账号',
    state: { set: true, account: { id: '456', name: null, verified: false, vip: null } },
    want: { status: '已保存登录信息，未经 QQ 音乐核实', details: ['账号 ID：456'] },
  },
]

describe('sourceAccountView', () => {
  for (const tc of cases) {
    it(tc.name, () => {
      expect(sourceAccountView(tc.state, 'QQ 音乐')).toEqual(tc.want)
    })
  }
})
