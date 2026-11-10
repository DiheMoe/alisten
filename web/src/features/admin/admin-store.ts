import type { ErrorTexts } from '@/core/errors'
import { ApiError, deleteAdminToken, loadAdminToken, saveAdminToken } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'

// 管理 token 无效、后台没有启用或网址不被服务器接受。
function rejectsToken(error: unknown): boolean {
  return error instanceof ApiError && (error.code === 'UNAUTHORIZED' || error.code === 'FORBIDDEN')
}

class AdminStore extends Store<{ signedIn: boolean }> {
  constructor() {
    super({ signedIn: false })
  }

  start(): void {
    this.setState({ signedIn: loadAdminToken() !== null })
  }

  signIn(token: string): void {
    saveAdminToken(token)
    this.setState({ signedIn: true })
  }

  signOut(): void {
    deleteAdminToken()
    this.setState({ signedIn: false })
  }

  // 失败时显示错误并返回 null。管理 token 被拒绝时退出登录。
  // 204 的接口成功时得到 undefined，调用方要与 null 比较，不能只看真假。
  async call<T>(request: () => Promise<T>, texts?: ErrorTexts): Promise<T | null> {
    try {
      return await request()
    }
    catch (error) {
      this.#report(error, texts)
      return null
    }
  }

  // 失败时抛出 Error，message 是给用户看的原因，由调用方就地显示，不弹通知。
  // 管理 token 被拒绝时例外，照常提示，因为退出登录后这一页就不显示了。
  async attempt<T>(request: () => Promise<T>, texts?: ErrorTexts): Promise<T> {
    try {
      return await request()
    }
    catch (error) {
      if (rejectsToken(error))
        this.#report(error, texts)
      throw new Error(errorMessage(error, texts))
    }
  }

  #report(error: unknown, texts?: ErrorTexts): void {
    // 同时进行的请求会一起失败，退出登录后不再重复提示。
    if (!this.state.signedIn)
      return
    notificationStore.error(errorMessage(error, texts))
    if (rejectsToken(error))
      this.signOut()
  }
}

export const adminStore = new AdminStore()
