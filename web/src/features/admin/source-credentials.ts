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
    credential: () => api.adminGetNeteaseCredential(),
    targets: [
      { label: '网易云音乐扫码', start: () => api.adminStartNeteaseLogin(), check: key => api.adminCheckNeteaseLogin({ key }) },
    ],
  },
  qq: {
    name: 'QQ 音乐',
    credential: () => api.adminGetQqCredential(),
    targets: [
      { label: 'QQ 扫码', start: () => api.adminStartQqLogin({ type: 'qq' }), check: key => api.adminCheckQqLogin({ type: 'qq', key }) },
      { label: '微信扫码', start: () => api.adminStartQqLogin({ type: 'wx' }), check: key => api.adminCheckQqLogin({ type: 'wx', key }) },
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

// 分类栏要根据登录状态给“音源”加红点，所以后台页一打开就查询，不等打开这个分类。
export function useSourceCredentials(): Record<SourceId, Credential> {
  return {
    netease: useCredential(sources.netease),
    qq: useCredential(sources.qq),
    bilibili: useCredential(sources.bilibili),
  }
}
