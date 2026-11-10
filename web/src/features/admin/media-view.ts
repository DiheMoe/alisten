import type { StoredMedia } from '@/core/realtime'
import { bookTitle } from '@/shared/lib/book-title'

const MiB = 1024 * 1024
const GiB = 1024 * MiB
const minute = 60 * 1000
const hour = 60 * minute
const day = 24 * hour

// 地址里的搜索词变了之后输入框显示什么：它就是输入框自己提交的值（去掉了首尾空白）时保留原样的输入，比如末尾刚打的空格；被别处改掉时（清除搜索、后退）换成地址里的。
export function searchDraft(draft: string, q: string): string {
  return draft.trim() === q ? draft : q
}

export function formatSize(bytes: number): string {
  return bytes < GiB ? `${(bytes / MiB).toFixed(1)} MiB` : `${(bytes / GiB).toFixed(1)} GiB`
}

export function formatStoredDate(ms: number, now: number): string {
  const date = new Date(ms)
  const monthDay = `${date.getMonth() + 1} 月 ${date.getDate()} 日`
  return date.getFullYear() === new Date(now).getFullYear() ? monthDay : `${date.getFullYear()} 年 ${monthDay}`
}

export function formatPlayed(ms: number | null, now: number): string {
  if (ms === null)
    return '从未播放'
  const elapsed = now - ms
  if (elapsed < minute)
    return '刚刚'
  if (elapsed < hour)
    return `${Math.floor(elapsed / minute)} 分钟前`
  if (elapsed < day)
    return `${Math.floor(elapsed / hour)} 小时前`
  const days = Math.floor(elapsed / day)
  if (days < 30)
    return `${days} 天前`
  if (days < 365)
    return `${Math.floor(days / 30)} 个月前`
  return `${Math.floor(days / 365)} 年前`
}

export function mediaOrigin(item: StoredMedia): string {
  switch (item.source) {
    case 'bilibili':
      return '哔哩哔哩'
    case 'netease':
      return '网易云音乐'
    case 'qq':
      return 'QQ 音乐'
    case 'link':
      try {
        return new URL(item.link).host
      }
      catch {
        return item.link
      }
  }
}

export function mediaByline(item: StoredMedia): string {
  const origin = mediaOrigin(item)
  return item.artist ? `${item.artist} · ${origin}` : origin
}

// 不存在的歌（比如在别的标签页里已经删掉了）也算删除：对管理员来说结果一样。
export function removalMessage(requested: number, inUse: number): string {
  const busy = `${inUse} 首正在房间里播放，没有删除`
  if (inUse === requested)
    return busy
  const removed = `已删除 ${requested - inUse} 首`
  return inUse ? `${removed}；${busy}` : removed
}

export function deleteConfirmText(items: StoredMedia[]): string {
  const size = formatSize(items.reduce((sum, item) => sum + item.size, 0))
  return items.length === 1
    ? `删除${bookTitle(items[0]!.title)}（${size}）的音频和封面后，再点这首歌会重新下载。`
    : `删除选中的 ${items.length} 首歌（共 ${size}）的音频和封面后，再点这些歌会重新下载。`
}
