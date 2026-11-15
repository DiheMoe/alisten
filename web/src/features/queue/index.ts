import { lazyPage } from '@/shared/ui'

export { voteSkip } from './actions'
export const QueuePage = lazyPage(() => import('./queue-page').then(module => module.QueuePage))
