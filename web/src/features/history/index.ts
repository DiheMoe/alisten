import { lazyPage } from '@/shared/ui'

export const HistoryPage = lazyPage(() => import('./history-page').then(module => module.HistoryPage))
export { historyStore } from './history-store'
