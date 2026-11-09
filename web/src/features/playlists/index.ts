import { lazyPage } from '@/shared/ui'

export { setAccountSettingsOpener } from './account-settings'
export { playlistsStore } from './playlists-store'
export { saveToPlaylistLabel } from './save-label'
export { SaveToPlaylistButton, SaveToPlaylistDialog } from './save-to-playlist'
export const PlaylistsPage = lazyPage(() => import('./playlists-page').then(module => module.PlaylistsPage))
export const MyPlaylistPage = lazyPage(() => import('./my-playlist-page').then(module => module.MyPlaylistPage))
