import type { QrLoginTarget } from './qr-login-dialog'
import type { CredentialState, SourceCredential } from './source-account-view'
import { api } from '@/core/api'
import { useResource } from '@/shared/lib/use-resource'
import { adminStore } from './admin-store'

export interface Source {
  name: string
  credential: () => Promise<SourceCredential>
  targets: QrLoginTarget[]
}

export type SourceId = 'netease' | 'qq' | 'bilibili'

export const sources: Record<SourceId, Source> = {
  netease: {
    name: '网易云音乐',
    credential: () => api.adminGetNetEaseCredential(),
    targets: [
      { label: '网易云音乐扫码', start: () => api.adminStartNetEaseLogin(), check: key => api.adminCheckNetEaseLogin({ key }) },
    ],
  },
  qq: {
    name: 'QQ 音乐',
    credential: () => api.adminGetQQCredential(),
    targets: [
      { label: 'QQ 扫码', start: () => api.adminStartQQLogin({ type: 'qq' }), check: key => api.adminCheckQQLogin({ type: 'qq', key }) },
      { label: '微信扫码', start: () => api.adminStartQQLogin({ type: 'wx' }), check: key => api.adminCheckQQLogin({ type: 'wx', key }) },
    ],
  },
  bilibili: {
    name: '哔哩哔哩',
    credential: () => api.adminGetBilibiliCredential(),
    targets: [
      { label: '哔哩哔哩扫码', start: () => api.adminStartBilibiliLogin(), check: key => api.adminCheckBilibiliLogin({ key }) },
    ],
  },
}

export interface Credential {
  state: CredentialState
  reload: () => void
}

function useCredential(source: Source): Credential {
  return useResource(() => adminStore.attempt(source.credential), source)
}

// 分类栏要根据登录状态给“音乐平台”加红点，所以后台页一打开就查询，不等打开这个分类。
export function useSourceCredentials(): Record<SourceId, Credential> {
  return {
    netease: useCredential(sources.netease),
    qq: useCredential(sources.qq),
    bilibili: useCredential(sources.bilibili),
  }
}
