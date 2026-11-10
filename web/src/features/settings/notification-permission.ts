import { Store } from '@/shared/lib/store'

interface NotificationPermissionState {
  // 浏览器不通知权限的变化，构造时和每次申请之后各读一次。
  permission: NotificationPermission
  waiting: boolean
  // 最近一次申请没有拿到授权。
  refused: boolean
}

// 浏览器的授权提示可能一直不返回，比如用户没理会，或者浏览器把提示收进了地址栏。所以等待期间可以放弃。
// 放弃之后提示才返回时，只更新权限，不算这次申请的结果。
export class NotificationPermissionRequest extends Store<NotificationPermissionState> {
  #latest = 0

  constructor() {
    super({ permission: Notification.permission, waiting: false, refused: false })
  }

  // 只能在用户操作的事件里调用，因为 Safari 和 Firefox 不接受其他时候发起的权限申请。
  // 返回这次申请是否拿到了授权。等待期间被放弃，或者又发起了新的申请时，返回 false。
  async request(): Promise<boolean> {
    const id = ++this.#latest
    this.setState({ waiting: true, refused: false })
    const permission = await Notification.requestPermission()
    if (id !== this.#latest) {
      this.setState({ permission })
      return false
    }
    this.setState({ permission, waiting: false, refused: permission !== 'granted' })
    return permission === 'granted'
  }

  abandon(): void {
    this.#latest++
    this.setState({ waiting: false, refused: false })
  }
}
