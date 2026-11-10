import { useEffect, useState } from 'react'
import { api } from '@/core/api'

export type Site = { status: 'loading' } | { status: 'failed' } | { status: 'ready', registrationOpen: boolean, githubLogin: boolean }

export function useSite(): { site: Site, reload: () => void } {
  const [site, setSite] = useState<Site>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let active = true
    api.getSite().then(({ registrationOpen, githubLogin }) => {
      if (active)
        setSite({ status: 'ready', registrationOpen, githubLogin })
    }, () => {
      if (active)
        setSite({ status: 'failed' })
    })
    return () => {
      active = false
    }
  }, [attempt])
  return {
    site,
    reload: () => {
      setSite({ status: 'loading' })
      setAttempt(count => count + 1)
    },
  }
}
