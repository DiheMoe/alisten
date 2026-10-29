import { lazyPage } from '@/shared/ui'

export { HeaderSearch } from './header-search'
export { canOpenPlaylists, defaultSearchSource, keywordSearch, searchStore } from './search-store'
export type { SearchQuery } from './search-store'
export const SearchPage = lazyPage(() => import('./search-page').then(module => module.SearchPage))
export const PlaylistPage = lazyPage(() => import('./search-page').then(module => module.PlaylistPage))
