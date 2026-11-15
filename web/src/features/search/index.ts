import { lazyPage } from '@/shared/ui'

export type { SearchCategory, SearchRecord, SearchSource } from './records'
export { isSearchSource } from './records'
export { SearchBox } from './search-box'
export { searchHistoryStore, searchLimits } from './search-history-store'
export { defaultSearchSource, keywordSearch, searchStore } from './search-store'
export type { SearchQuery } from './search-store'
export const SearchPage = lazyPage(() => import('./search-page').then(module => module.SearchPage))
export const PlaylistPage = lazyPage(() => import('./search-page').then(module => module.PlaylistPage))
