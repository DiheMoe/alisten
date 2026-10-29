import { lazyPage } from '@/shared/ui'

export { enqueue, voteSkip } from './actions'
export { sourceName } from './source'
export const QueuePage = lazyPage(() => import('./queue-page').then(module => module.QueuePage))
