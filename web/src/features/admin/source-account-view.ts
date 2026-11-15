import type { StatusTone } from './section-head'
import type { AdminGetBilibiliCredentialResponse, AdminGetNeteaseCredentialResponse, AdminGetQQCredentialResponse } from '@/core/api'
import type { ResourceState } from '@/shared/lib/resource'

export type SourceCredential = AdminGetNeteaseCredentialResponse | AdminGetQQCredentialResponse | AdminGetBilibiliCredentialResponse

export type CredentialState = ResourceState<SourceCredential>

// 接在中文后面的平台名以英文开头时，前面留一个空格，如「已登录 QQ 音乐」。
export function afterChinese(platform: string): string {
  return /^[a-z0-9]/i.test(platform) ? ` ${platform}` : platform
}

export function needsSignIn(state: CredentialState): boolean {
  return state.status === 'loaded' && !state.value.account?.verified
}

export function sourceAccountView(state: CredentialState, platform: string): { status: string, tone: StatusTone, detail: string } {
  if (state.status === 'loading')
    return { status: '正在查询', tone: 'neutral', detail: '' }
  if (state.status === 'failed')
    return { status: '查询失败', tone: 'neutral', detail: state.error }
  if (!state.value.set)
    return { status: '未登录', tone: 'danger', detail: '' }
  if (state.value.account === null)
    return { status: '未识别账号', tone: 'danger', detail: '已保存登录信息，但没有识别出账号。' }

  const { account } = state.value
  const details = [account.name ?? `ID ${account.id}`]
  if (account.vip !== null)
    details.push(account.vip ? '会员' : '非会员')
  if (!account.verified)
    details.push(`已保存登录信息，未经${afterChinese(platform)}核实`)
  return { status: account.verified ? '已登录' : '未经核实', tone: account.verified ? 'success' : 'danger', detail: details.join(' · ') }
}
