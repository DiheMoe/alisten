import type { QrLoginTarget } from './qr-login-dialog'
import type { CredentialState } from './source-account-view'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { adminStore } from './admin-store'

export interface Source {
  name: string
  credential: () => Promise<Exclude<CredentialState, Error | undefined>>
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
  const [state, setState] = useState<CredentialState>()
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let active = true
    adminStore.attempt(source.credential).then((result) => {
      if (active)
        setState(result)
    }, (error: Error) => {
      if (active)
        setState(error)
    })
    return () => {
      active = false
    }
  }, [source, attempt])
  return {
    state,
    reload: () => {
      setState(undefined)
      setAttempt(n => n + 1)
    },
  }
}

// 分类栏要根据登录状态给“音源”加红点，所以后台页一打开就查询，不等打开这个分类。
export function useSourceCredentials(): Record<SourceId, Credential> {
  return {
    netease: useCredential(sources.netease),
    qq: useCredential(sources.qq),
    bilibili: useCredential(sources.bilibili),
  }
}
