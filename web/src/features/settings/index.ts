import { lazyPage } from '@/shared/ui'

export { appearanceStore } from './appearance-store'
export { lyricsDisplayStore } from './lyrics-display-store'
export { performanceStore } from './performance-store'
export type { SettingsSection } from './settings-page'
export const SettingsPage = lazyPage(() => import('./settings-page').then(module => module.SettingsPage))
export const PairPage = lazyPage(() => import('./pair-page').then(module => module.PairPage))
export const GitHubLoginPage = lazyPage(() => import('./github-login-page').then(module => module.GitHubLoginPage))
