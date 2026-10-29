import type { ConnectionStatus } from '@/core/realtime'
import { errorText } from '@/core/errors'
import { roomStore } from '@/core/room'
import { notificationStore } from '@/shared/ui'

// 连接上的 INVALID_ARGUMENT 只可能是前端发出的报文不对，用户能做的只有刷新页面。
const connectionTexts = { INVALID_ARGUMENT: '连接房间时出错了，请刷新页面再试。' }

export function watchConnection(): void {
  let last: ConnectionStatus | null = null
  let outageNotified = false
  roomStore.addEventListener('change', () => {
    const status = roomStore.state.status
    if (status === last)
      return
    last = status
    if (status === 'open') {
      outageNotified = false
      notificationStore.success('已连上房间')
    }
    else if (status === 'reconnecting' && !outageNotified) {
      outageNotified = true
      notificationStore.error('与房间的连接中断了，正在自动重连。', 5000)
    }
  })
  // 握手时票据被拒绝会以 4401 断开，进房流程随后重新打开加入弹窗，不另外弹通知。
  roomStore.errors.on((error) => {
    if (error.code === 'UNAUTHORIZED')
      return
    const message = errorText(error.code, error.message, connectionTexts)
    // 协议版本不一致时页面随后自动刷新，用户不用处理。
    if (error.code === 'CLIENT_OUTDATED')
      notificationStore.info(message)
    else
      notificationStore.error(message)
  })
}
