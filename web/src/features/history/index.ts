import { lazyPage } from '@/shared/ui'

export const HistoryPage = lazyPage(() => import('./history-page').then(module => module.HistoryPage))
export { historyStore, searchLimits } from './history-store'
export type { SearchCategory, SearchRecord, SearchSource } from './records'
