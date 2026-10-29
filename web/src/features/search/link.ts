import type { RoomKind } from '@/core/realtime'
import { isLink } from '@/core/realtime'

export function linkOf(input: string): string | null {
  const link = input.trim()
  return isLink(link) ? link : null
}

// 临时房间只能点 B 站的链接：服务端把它们识别成 B 站音源的歌，其他链接是只能在常驻房间点的链接曲目。kind 为 undefined 时不在房间里。
export function pickableLink(input: string, kind: RoomKind | undefined): string | null {
  const link = linkOf(input)
  if (link === null || kind === undefined)
    return null
  return kind === 'persistent' || isBilibiliLink(link) ? link : null
}

export function isBilibiliLink(link: string): boolean {
  const host = new URL(link).hostname
  return host === 'b23.tv' || host === 'bilibili.com' || host.endsWith('.bilibili.com')
}
