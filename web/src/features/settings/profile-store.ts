import type { IdentityEvent } from '@/core/api'
import type { Member } from '@/core/realtime'
import { api, identityEvents, IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'

// 邮箱只保存在本机，用来在设置页回显；服务端只保存它的哈希。
const emailKey = 'alisten.email'

class ProfileStore extends Store<{ member: Member | null, email: string, failed: boolean }> {
  #loading: Promise<void> | null = null

  constructor() {
    super({ member: null, email: '', failed: false })
  }

  start(): void {
    this.setState({ email: localStorage.getItem(emailKey) ?? '' })
    identityEvents.on(this.#onIdentity)
  }

  load(): Promise<void> {
    this.#loading ??= this.#load().finally(() => {
      this.#loading = null
    })
    return this.#loading
  }

  // 返回 false 时，失败原因已经通过通知显示。
  async save(changes: { name?: string, email?: string }): Promise<boolean> {
    try {
      const { member } = await api.updateMe(changes)
      const email = changes.email ?? this.state.email
      localStorage.setItem(emailKey, email)
      this.setState({ member, email, failed: false })
      return true
    }
    catch (error) {
      // 输入框限制了昵称和邮箱的长度，INVALID_ARGUMENT 只剩去掉首尾空白之后为空的昵称。
      notificationStore.error(error instanceof IdentityRenewedError ? '个人资料没有保存，再保存一次。' : errorMessage(error, { INVALID_ARGUMENT: '昵称不能为空，也不能只有空格。' }))
      return false
    }
  }

  async #load(): Promise<void> {
    this.setState({ failed: false })
    try {
      this.setState({ member: (await api.getMe()).member, failed: false })
    }
    catch (error) {
      // 成员 token 被拒绝时身份已经更换，新的成员由身份事件设置。
      if (error instanceof IdentityRenewedError)
        return
      this.setState({ failed: true })
      throw error
    }
  }

  #onIdentity = (event: IdentityEvent): void => {
    switch (event.kind) {
      case 'rejected':
        // 本机保存的邮箱属于原来的身份。
        localStorage.removeItem(emailKey)
        this.setState({ member: null, email: '', failed: false })
        return
      case 'renewed':
        this.setState({ member: event.member, failed: false })
        notificationStore.info('原来的身份已经失效，已为你换了一个新的身份。', 8000)
        return
      case 'renewalFailed':
        this.setState({ failed: true })
        notificationStore.info('原来的身份已经失效，新的身份暂时没有获取到，下次操作时会再试。', 8000)
    }
  }
}

export const profileStore = new ProfileStore()
