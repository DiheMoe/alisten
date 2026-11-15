import type { GetSiteResponse } from '@/core/api'
import type { ResourceState } from '@/shared/lib/resource'
import { api } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { useResource } from '@/shared/lib/use-resource'

export type Site = ResourceState<GetSiteResponse>

export function useSite() {
  return useResource(() => api.getSite().catch((error: unknown) => {
    throw new Error(errorMessage(error))
  }))
}
