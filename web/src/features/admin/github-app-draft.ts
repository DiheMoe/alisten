import type { AdminSetGithubCredentialRequest } from '@/core/api'

// 从 GitHub 复制时容易带上首尾空白，服务端不接受空白。
export function githubAppDraft(clientId: string, clientSecret: string): AdminSetGithubCredentialRequest | null {
  const draft = { clientId: clientId.trim(), clientSecret: clientSecret.trim() }
  return draft.clientId !== '' && draft.clientSecret !== '' ? draft : null
}
