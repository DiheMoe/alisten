import type { SearchQuery } from './search-store'
import { SearchBox } from './search-box'

export function HeaderSearch({ onSearch }: { onSearch: (query: SearchQuery) => void }) {
  return <SearchBox className="header-search" recent onSearch={onSearch} />
}
