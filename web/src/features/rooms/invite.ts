import type { RoomListing } from '@/core/realtime'
import { notificationStore } from '@/shared/ui'

export async function copyInviteLink(room: Pick<RoomListing, 'id' | 'slug'>): Promise<void> {
  // 链接里不带密码，打开后由用户输入。
  const url = `${window.location.origin}/r/${room.slug ?? room.id}`
  try {
    await navigator.clipboard.writeText(url)
    notificationStore.success('已复制邀请链接')
  }
  catch {
    // 非安全上下文没有剪贴板接口，用户也可能拒绝授权。
    notificationStore.info(`请手动复制邀请链接：${url}`, 8000)
  }
}
