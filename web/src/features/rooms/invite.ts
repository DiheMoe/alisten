import type { RoomListing } from '@/core/realtime'
import { copyText } from '@/shared/lib/clipboard'
import { notificationStore } from '@/shared/lib/notification-store'

export async function copyInviteLink(room: Pick<RoomListing, 'id' | 'slug'>): Promise<void> {
  // 链接里不带密码，打开后由用户输入。
  const url = `${window.location.origin}/r/${room.slug ?? room.id}`
  if (await copyText(url))
    notificationStore.success('已复制邀请链接')
  else
    notificationStore.info(`请手动复制邀请链接：${url}`, 8000)
}
