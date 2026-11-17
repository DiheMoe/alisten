import type { ConnectionStatus } from '@/core/realtime'
import { errorText } from '@/core/errors'
import { roomStore } from '@/core/room'
import { notificationStore } from '@/shared/lib/notification-store'

// 连接上的 INVALID_ARGUMENT 只可能是前端发出的报文不对，用户能做的只有刷新页面。
const connectionTexts = { INVALID_ARGUMENT: '连接房间时出错了，请刷新页面再试。' }

export function watchConnection(): void {
  let last: ConnectionStatus | null = null
  let outageNotified = false
  let connectionLimitNotified = false
  roomStore.addEventListener('change', () => {
    const status = roomStore.state.status
    if (status === last)
      return
    last = status
    if (status === 'open') {
      outageNotified = false
      connectionLimitNotified = false
      notificationStore.success('已连上房间')
    }
    else if (status === 'reconnecting' && !outageNotified) {
      outageNotified = true
      notificationStore.error('与房间的连接中断了，正在自动重连。', 5000)
    }
  })
  // 握手时票据被拒绝，连接会以 4401 断开，随后进房流程会重新打开加入弹窗，所以不另外弹通知。
  // 房间已满时进房流程停在“房间已满”的页面，页面上显示的就是这条文案，所以也不弹通知。
  roomStore.errors.on((error) => {
    if (error.code === 'UNAUTHORIZED' || error.code === 'ROOM_FULL')
      return
    // 连接太多时连接会隔一会儿自动重连，每次重连都会再收到这个错误，所以连上之前只提示一次。
    if (error.code === 'TOO_MANY_CONNECTIONS') {
      if (connectionLimitNotified)
        return
      connectionLimitNotified = true
    }
    const message = errorText(error.code, error.message, connectionTexts)
    // 协议版本不一致时页面随后自动刷新，用户不用处理。
    if (error.code === 'CLIENT_OUTDATED')
      notificationStore.info(message)
    else
      notificationStore.error(message)
  })
}
