import type { FinishGithubLoginResponse } from '@/core/api'
import type { GitHubIntent } from '@/core/realtime'
import { api, IdentityRenewedError } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { notificationStore } from '@/shared/ui'

// GitHub 跳回来时仍在发起授权的这个标签页，所以 nonce 存在 sessionStorage。这样别的标签页、别的浏览器拿到兑换码，也拿不到 nonce。
// 意图也存在 sessionStorage 里，回来之后据此决定接下来做什么。
const nonceKey = 'alisten.github-nonce'
const intentKey = 'alisten.github-intent'

const failures: Record<string, string> = {
  denied: '你在 GitHub 上取消了授权。',
  expired: 'GitHub 授权已经过期，请回到设置重新发起。',
}

function randomNonce(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32))
  return btoa(String.fromCharCode(...bytes)).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '')
}

// 返回 GitHub 的授权地址，调用方随后跳转过去。绑定要带上用户输入的当前密码。
export async function startGithubLogin(intent: GitHubIntent, currentPassword?: string): Promise<string> {
  const nonce = randomNonce()
  sessionStorage.setItem(nonceKey, nonce)
  sessionStorage.setItem(intentKey, intent)
  const { url } = await api.startGithubLogin({ intent, nonce, currentPassword })
  return url
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

export type GithubLanding
  = | { kind: 'exchange', code: string, nonce: string, intent: GitHubIntent }
    | { kind: 'failed', message: string }

let landed: { fragment: string, landing: GithubLanding } | null = null

// fragment 是落地页地址中 # 之后的部分：code=<兑换码> 或 error=<错误码>。保存的 nonce 取出即删除，所以每次发起只能兑换一次。
// 同一次落地重复读取时（开发模式下组件会挂载两次）返回同样的结果。
export function takeGithubLanding(fragment: string): GithubLanding {
  if (landed?.fragment === fragment)
    return landed.landing
  const nonce = sessionStorage.getItem(nonceKey)
  const intent = sessionStorage.getItem(intentKey) as GitHubIntent | null
  sessionStorage.removeItem(nonceKey)
  sessionStorage.removeItem(intentKey)
  const params = new URLSearchParams(fragment)
  const code = params.get('code')
  const error = params.get('error')
  let landing: GithubLanding
  if (code)
    landing = nonce && intent ? { kind: 'exchange', code, nonce, intent } : { kind: 'failed', message: '这次 GitHub 登录不是在这个页面发起的，请回到设置重新发起。' }
  else if (error)
    landing = { kind: 'failed', message: failures[error] ?? '没有从 GitHub 确认你的身份，请稍后再试。' }
  else
    landing = { kind: 'failed', message: '没有收到 GitHub 的授权结果，请回到设置重新发起。' }
  landed = { fragment, landing }
  return landing
}

let finishing: { code: string, result: Promise<FinishGithubLoginResponse> } | null = null
let credentialsPending = false

// 兑换码只能用一次，所以同一个兑换码重复调用时，返回同一次请求的结果。
export function finishGithubLogin(code: string, nonce: string, intent: GitHubIntent): Promise<FinishGithubLoginResponse> {
  if (finishing?.code !== code) {
    let result = api.finishGithubLogin({ code, nonce })
    if (intent === 'verify') {
      result = result.then((response) => {
        credentialsPending = true
        return response
      })
    }
    finishing = { code, result }
  }
  return finishing.result
}

// 用 GitHub 确认了身份之后，接下来设置用户名和密码。落地页回到设置页，由设置页打开对话框。用户名和密码不在跳转期间保存，回来之后才填写。
// 只返回一次 true。
export function takeCredentialsPrompt(): boolean {
  const pending = credentialsPending
  credentialsPending = false
  return pending
}
