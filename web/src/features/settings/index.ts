import { lazyPage } from '@/shared/ui'

export { appearanceStore } from './appearance-store'
export { performanceStore } from './performance-store'
export { profileStore } from './profile-store'
export const SettingsPage = lazyPage(() => import('./settings-page').then(module => module.SettingsPage))
