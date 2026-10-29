import type { AdminGetBilibiliCredentialResponse, AdminGetNeteaseCredentialResponse, AdminGetQQCredentialResponse } from '@/core/api'

export type CredentialState = AdminGetNeteaseCredentialResponse | AdminGetQQCredentialResponse | AdminGetBilibiliCredentialResponse | null | undefined

// 接在中文后面的平台名以英文开头时，前面留一个空格，如「已登录 QQ 音乐」。
export function afterChinese(platform: string): string {
  return /^[a-z0-9]/i.test(platform) ? ` ${platform}` : platform
}

export function sourceAccountView(state: CredentialState, platform: string): { status: string, details: string[] } {
  if (state === undefined)
    return { status: '正在查询…', details: [] }
  if (state === null)
    return { status: '查询失败', details: [] }
  if (!state.set)
    return { status: '未登录', details: [] }
  if (state.account === null)
    return { status: '已保存登录信息，未识别到账号', details: [] }

  const { account } = state
  const details: string[] = []
  if (account.name !== null)
    details.push(`昵称：${account.name}`)
  details.push(`账号 ID：${account.id}`)
  if (account.vip !== null)
    details.push(`会员状态：${account.vip ? '会员' : '非会员'}`)
  return {
    status: account.verified ? '已登录' : `已保存登录信息，未经${afterChinese(platform)}核实`,
    details,
  }
}
