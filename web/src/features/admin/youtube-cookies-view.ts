import type { AdminGetYoutubeCredentialResponse } from '@/core/api'

export type YoutubeCredentialState = AdminGetYoutubeCredentialResponse | null | undefined

export const youtubeCookiesText = {
  plaintextWarning: '当前连接不是 HTTPS，填写的 cookies 会以明文在网络上传输，只在可信网络中填写。',
  tooLarge: 'cookies 太长了，上限约 64 KiB，只导出 YouTube 的 cookies 再试。',
}

// 服务端限制的是整个请求体的字节数，请求体是 JSON 编码的 { cookies }。
const requestLimitBytes = 64 * 1024

export function youtubeCookiesTooLarge(cookies: string): boolean {
  return new TextEncoder().encode(JSON.stringify({ cookies })).length > requestLimitBytes
}

export function canSubmitYoutubeCookies(cookies: string, busy: boolean): boolean {
  return !busy && cookies.trim() !== '' && !youtubeCookiesTooLarge(cookies)
}

// 本机地址的流量不经过网络。
export function shouldWarnAboutPlaintext(location: Pick<Location, 'protocol' | 'hostname'>): boolean {
  if (location.protocol === 'https:')
    return false
  const host = location.hostname.toLowerCase()
  return !['localhost', '::1', '[::1]'].includes(host) && !host.endsWith('.localhost') && !/^127(?:\.\d{1,3}){3}$/.test(host)
}

export function youtubeCookiesView(state: YoutubeCredentialState): { status: string, updatedAtMs: number | null } {
  if (state === undefined)
    return { status: '正在查询…', updatedAtMs: null }
  if (state === null)
    return { status: '查询失败', updatedAtMs: null }
  if (!state.set)
    return { status: '未配置', updatedAtMs: null }
  return { status: '已配置', updatedAtMs: state.updatedAtMs }
}
