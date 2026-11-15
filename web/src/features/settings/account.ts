import type { GithubLanding } from './github'
import type { CreatePairingCodeResponse, FinishGithubLoginResponse } from '@/core/api'
import type { GitHubIntent } from '@/core/realtime'
import { api, ApiError, IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { profileStore, switchIdentity } from '@/core/profile'
import { AccountPasswordLimits } from '@/core/realtime'
import { notificationStore } from '@/shared/lib/notification-store'
import { finishGithubLogin, startGithubLogin } from './github'
import { isPairingCode } from './pairing'
import { loginWithPassword } from './password-login'

const credentialTexts = { INVALID_ARGUMENT: `用户名要 3–32 位小写字母、数字、_ 或 -，密码要 ${AccountPasswordLimits.minLength}–${AccountPasswordLimits.maxLength} 位。` }
const pairingCodeFormat = '配对码是 8 位字母和数字，不含 0、O、1、I。'

// credentials：只绑定了 GitHub 的账号补设用户名和密码，用的是注册接口。
export type CredentialsMode = 'register' | 'login' | 'credentials'

// 返回对话框接下来要做的事。close 是关闭对话框。retry 是留着对话框重试，失败原因已经通过通知显示。
// reloading 是登录成功，页面随即重新加载，对话框保持忙碌。
export async function submitCredentials(mode: CredentialsMode, username: string, password: string): Promise<'close' | 'retry' | 'reloading'> {
  const register = mode !== 'login'
  try {
    if (!register) {
      switchIdentity(await loginWithPassword(username, password))
      return 'reloading'
    }
    const { account } = await api.register({ username, password })
    notificationStore.success(mode === 'credentials' ? '已设置用户名和密码，其他设备已退出登录' : '已注册')
    void profileStore.registered(account)
    return 'close'
  }
  catch (error) {
    // 用 GitHub 确认身份已经过期，在对话框里重试也没用，所以关掉对话框，从按钮重新确认。
    if (mode === 'credentials' && error instanceof ApiError && error.code === 'FORBIDDEN') {
      notificationStore.error('用 GitHub 确认身份已经超过 10 分钟，请在“用户名和密码”一行再点一次“设置”。')
      return 'close'
    }
    notificationStore.error(error instanceof IdentityRenewedError ? (register ? '没有注册成功，再试一次。' : '没有登录，再试一次。') : errorMessage(error, credentialTexts))
    return 'retry'
  }
}

// 返回 false 时，失败原因已经通过通知显示。
export async function changePassword(currentPassword: string, newPassword: string): Promise<boolean> {
  try {
    await api.changePassword({ currentPassword, newPassword })
  }
  catch (error) {
    notificationStore.error(error instanceof IdentityRenewedError ? '密码没有修改，再试一次。' : errorMessage(error, { INVALID_ARGUMENT: `密码要 ${AccountPasswordLimits.minLength} 到 ${AccountPasswordLimits.maxLength} 位。` }))
    return false
  }
  notificationStore.success('已修改密码，其他设备已退出登录')
  return true
}

// 返回 false 时，失败原因已经通过通知显示。成功时页面随即跳到 GitHub。
export async function goToGitHub(intent: GitHubIntent, currentPassword?: string): Promise<boolean> {
  try {
    window.location.assign(await startGithubLogin(intent, currentPassword))
    return true
  }
  catch (error) {
    notificationStore.error(error instanceof IdentityRenewedError ? '没有跳转到 GitHub，再试一次。' : errorMessage(error))
    return false
  }
}

// 返回 false 时，失败原因已经通过通知显示。
export async function unbindGithub(currentPassword: string): Promise<boolean> {
  const { account } = profileStore.state
  if (!account)
    return false
  try {
    await api.unbindGithub({ currentPassword })
  }
  catch (error) {
    notificationStore.error(error instanceof IdentityRenewedError ? '没有解绑，再试一次。' : errorMessage(error))
    return false
  }
  profileStore.updateAccount({ ...account, github: false })
  notificationStore.success('已解绑 GitHub')
  return true
}

// 返回 false 时，失败原因已经通过通知显示。
export async function logout(): Promise<boolean> {
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

// 在 GitHub 落地页上兑换授权。signal 中止表示已经离开落地页，这时兑换的结果作废。
// 成功时先调用 onDone，要换身份的话再换，因为换身份会重新加载页面。失败时返回给用户看的原因，其他情况返回 null。
export async function completeGithubLogin({ code, nonce, intent }: Extract<GithubLanding, { kind: 'exchange' }>, signal: AbortSignal, onDone: () => void): Promise<string | null> {
  let result: FinishGithubLoginResponse
  try {
    result = await finishGithubLogin(code, nonce, intent)
  }
  catch (error) {
    if (signal.aborted)
      return null
    return error instanceof IdentityRenewedError ? 'GitHub 登录没有完成，请回到设置重新发起。' : errorMessage(error)
  }
  if (signal.aborted)
    return null
  if (result.memberToken) {
    onDone()
    switchIdentity(result.memberToken)
    return null
  }
  // 确认身份不修改账号，回到设置页接着设置用户名和密码。
  if (intent === 'verify') {
    onDone()
    return null
  }
  // 注册或绑定时，成员和成员 token 都不变，不必重新加载。能绑定 GitHub 的只有设置了用户名的账号，注册得到的账号没有用户名。
  void profileStore.registered(result.account)
  notificationStore.success(result.account.username ? '已绑定 GitHub' : '已用 GitHub 注册')
  onDone()
  return null
}

// 失败时抛出 Error，message 是给用户看的原因。
export async function createPairingCode(): Promise<CreatePairingCodeResponse> {
  try {
    return await api.createPairingCode()
  }
  catch (error) {
    throw new Error(error instanceof IdentityRenewedError ? '没有生成配对码，再试一次。' : errorMessage(error))
  }
}

// 格式不对时返回 false，原因已经通过通知显示。
export function checkPairingCode(code: string): boolean {
  if (isPairingCode(code))
    return true
  notificationStore.error(pairingCodeFormat)
  return false
}

// 兑换配对码就是登录。成功时返回新会话的成员 token，由调用方换身份。返回 null 时，失败原因已经通过通知显示。
export async function redeemPairingCode(code: string): Promise<string | null> {
  try {
    const { memberToken } = await api.redeemPairingCode({ code })
    return memberToken
  }
  catch (error) {
    notificationStore.error(error instanceof IdentityRenewedError ? '没有登录，再试一次。' : errorMessage(error, { INVALID_ARGUMENT: pairingCodeFormat }))
    return null
  }
}
