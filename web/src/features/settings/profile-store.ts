import type { IdentityEvent } from '@/core/api'
import type { Account, Member } from '@/core/realtime'
import { api, ApiError, deleteAllTickets, deleteMemberToken, followMemberTokenChanges, identityEvents, IdentityRenewedError, saveMemberToken } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'

// 游客的邮箱只保存在本机，服务端只保存它的哈希；已注册账号的邮箱保存在账号里。
const emailKey = 'alisten.email'

interface ProfileState {
  member: Member | null
  account: Account | null
  email: string
  // 是否记录播放历史，保存在服务端；还没有读到时为 null。
  recordListens: boolean | null
  failed: boolean
}

class ProfileStore extends Store<ProfileState> {
  #loading: Promise<void> | null = null

  constructor() {
    super({ member: null, account: null, email: '', recordListens: null, failed: false })
  }

  start(): void {
    this.setState({ email: localStorage.getItem(emailKey) ?? '' })
    identityEvents.on(this.#onIdentity)
    // 其他标签页登录或退出之后，本标签页的资料、房间连接都属于旧身份，重新加载页面换成新身份。
    followMemberTokenChanges(() => window.location.reload())
  }

  load(): Promise<void> {
    this.#loading ??= this.#load().finally(() => {
      this.#loading = null
    })
    return this.#loading
  }

  // 本机已有的邮箱存进新账号，存失败只提示，不影响注册；本机的副本保留，下次加载时重试。
  async registered(account: Account): Promise<void> {
    this.setState({ account })
    await this.#moveLocalEmail()
  }

  // 返回 false 时，失败原因已经通过通知显示。
  async logout(): Promise<boolean> {
    try {
      await api.logout()
      switchIdentity(null)
    }
    catch (error) {
      // 账号在这台设备上的会话已经失效，客户端也已经换成新的游客，退出登录的目的已经达到。
      if (error instanceof IdentityRenewedError) {
        window.location.reload()
        return true
      }
      notificationStore.error(errorMessage(error))
      return false
    }
    return true
  }

  // 返回 false 时，失败原因已经通过通知显示。
  async save(changes: { name?: string, email?: string }): Promise<boolean> {
    return await this.#update(changes) === null
  }

  // 返回 false 时，失败原因已经通过通知显示。
  async setRecordListens(on: boolean): Promise<boolean> {
    try {
      await (on ? api.enableListenRecording() : api.disableListenRecording())
      this.setState({ recordListens: on })
      return true
    }
    catch (error) {
      notificationStore.error(error instanceof IdentityRenewedError ? '设置没有保存，再保存一次。' : errorMessage(error))
      return false
    }
  }

  // 失败时返回错误，失败原因已经通过通知显示。
  async #update(changes: { name?: string, email?: string }): Promise<unknown> {
    try {
      const { member } = await api.updateMe(changes)
      const { account } = this.state
      if (account) {
        const email = changes.email?.trim() ?? this.state.email
        // 用户对账号邮箱的修改是最终决定，本机残留的副本不能再覆盖它。
        if (changes.email !== undefined)
          localStorage.removeItem(emailKey)
        this.setState({ member, email, account: { username: account.username, ...(email && { email }) }, failed: false })
      }
      else {
        const email = changes.email ?? this.state.email
        localStorage.setItem(emailKey, email)
        this.setState({ member, email, failed: false })
      }
      return null
    }
    catch (error) {
      // 客户端不能按服务端的 message 区分是哪个字段不合格。
      notificationStore.error(error instanceof IdentityRenewedError ? '个人资料没有保存，再保存一次。' : errorMessage(error, { INVALID_ARGUMENT: '昵称不能为空；邮箱要写成 name@example.com 这样的格式。' }))
      return error
    }
  }

  async #load(): Promise<void> {
    this.setState({ failed: false })
    try {
      const { member, account, recordListens } = await api.getMe()
      this.setState({ member, account: account ?? null, email: account ? account.email ?? '' : localStorage.getItem(emailKey) ?? '', recordListens, failed: false })
      if (account && !account.email)
        await this.#moveLocalEmail()
    }
    catch (error) {
      // 成员 token 被拒绝时身份已经更换，新的成员由身份事件设置。
      if (error instanceof IdentityRenewedError)
        return
      this.setState({ failed: true })
      throw error
    }
  }

  async #moveLocalEmail(): Promise<void> {
    const email = localStorage.getItem(emailKey)
    if (!email)
      return
    const error = await this.#update({ email })
    // 服务端不接受的副本重试也不会成功，删掉免得每次加载都报错；其他错误（网络、服务端故障）留到下次再试。
    if (error instanceof ApiError && error.code === 'INVALID_ARGUMENT')
      localStorage.removeItem(emailKey)
  }

  #onIdentity = (event: IdentityEvent): void => {
    switch (event.kind) {
      case 'rejected':
        // 本机保存的邮箱属于原来的身份。
        localStorage.removeItem(emailKey)
        this.setState({ member: null, account: null, email: '', recordListens: null, failed: false })
        return
      case 'renewed':
        // 新的游客按默认记录播放历史。
        this.setState({ member: event.member, account: null, recordListens: true, failed: false })
        notificationStore.info('原来的身份已经失效，已为你换了一个新的身份。', 8000)
        return
      case 'renewalFailed':
        this.setState({ failed: true })
        notificationStore.info('原来的身份已经失效，新的身份暂时没有获取到，下次操作时会再试。', 8000)
    }
  }
}

export const profileStore = new ProfileStore()

// 换了身份之后，进房票据和本机保存的邮箱都属于原来的身份，一并清除；重新加载页面，让房间连接和各处的状态都从新身份开始。
export function switchIdentity(token: string | null): void {
  if (token === null)
    deleteMemberToken()
  else
    saveMemberToken(token)
  deleteAllTickets()
  localStorage.removeItem(emailKey)
  window.location.reload()
}
