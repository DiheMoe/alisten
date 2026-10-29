import { lazyPage } from '@/shared/ui'

export { chatStore } from './chat-store'
export const ChatPanel = lazyPage(() => import('./chat-panel').then(module => module.ChatPanel))
