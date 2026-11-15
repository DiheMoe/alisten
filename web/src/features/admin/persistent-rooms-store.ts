import type { RoomListing } from '@/core/realtime'
import { api } from '@/core/api'
import { Resource } from '@/shared/lib/resource'
import { adminStore } from './admin-store'

function listRooms(): Promise<RoomListing[]> {
  return adminStore.attempt(() => api.adminListRooms()).then(result => result.items)
}

// 每打开一次常驻房间页就新建一个 PersistentRoomsStore，这样离开再回来时会重新读取。
export class PersistentRoomsStore {
  readonly rooms = new Resource<RoomListing[]>()

  open(): void {
    this.rooms.open(listRooms)
  }

  close(): void {
    this.rooms.close()
  }

  refresh(): void {
    this.rooms.refresh(listRooms)
  }

  // 失败时原因已经通过通知显示，返回 false。
  async remove(id: string): Promise<boolean> {
    if (await adminStore.call(() => api.adminDeleteRoom(id)) === null)
      return false
    // 先在本地去掉这个房间，这样对话框关闭时那一行已经不在了，焦点才能落到下一行。之后再重新读取。
    const { current } = this.rooms.state
    this.rooms.set(current.status === 'loaded' ? current.value.filter(room => room.id !== id) : [])
    this.refresh()
    return true
  }
}
