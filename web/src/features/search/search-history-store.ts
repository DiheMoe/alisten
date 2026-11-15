import type { SearchCategory, SearchRecord, SearchSource } from './records'
import { Store } from '@/shared/lib/store'
import { parseSearches, recordSearch } from './records'

export const searchLimits = [5, 10, 20, 50]
const defaultSearchLimit = 10

const searchesKey = 'alisten.search-history'
const searchesEnabledKey = 'alisten.search-history.enabled'
const searchLimitKey = 'alisten.search-history.limit'

export interface SearchSettings {
  searchesEnabled: boolean
  searchLimit: number
}

interface SearchHistoryState extends SearchSettings {
  searches: SearchRecord[]
}

function loadSearchLimit(): number {
  const stored = Number(localStorage.getItem(searchLimitKey))
  return searchLimits.includes(stored) ? stored : defaultSearchLimit
}

class SearchHistoryStore extends Store<SearchHistoryState> {
  constructor() {
    super({ searches: [], searchesEnabled: true, searchLimit: defaultSearchLimit })
  }

  start(): void {
    const searchLimit = loadSearchLimit()
    const storedSearches = localStorage.getItem(searchesKey)
    const searches = parseSearches(storedSearches, searchLimit)
    this.setState({
      searches,
      searchesEnabled: localStorage.getItem(searchesEnabledKey) !== 'false',
      searchLimit,
    })
    if (storedSearches !== null && JSON.stringify(searches) !== storedSearches)
      this.#saveSearches(searches)
    window.addEventListener('storage', this.#onStorage)
  }

  recordSearch(query: string, source: SearchSource, category: SearchCategory): void {
    if (this.state.searchesEnabled)
      this.#saveSearches(recordSearch(this.state.searches, { query, source, category, searchedAtMs: Date.now() }, this.state.searchLimit))
  }

  clearSearches(): void {
    this.#saveSearches([])
  }

  updateSettings({ searchesEnabled, searchLimit }: SearchSettings): void {
    localStorage.setItem(searchesEnabledKey, String(searchesEnabled))
    localStorage.setItem(searchLimitKey, String(searchLimit))
    this.setState({ searchesEnabled, searchLimit })
    if (this.state.searches.length > searchLimit)
      this.#saveSearches(this.state.searches.slice(0, searchLimit))
  }

  // 其他标签页写入搜索记录时，本页跟着更新，这样本页之后的写入才不会覆盖那个标签页写入的记录。
  #onStorage = (event: StorageEvent): void => {
    if (event.key === searchesKey)
      this.setState({ searches: parseSearches(event.newValue, this.state.searchLimit) })
  }

  #saveSearches(searches: SearchRecord[]): void {
    localStorage.setItem(searchesKey, JSON.stringify(searches))
    this.setState({ searches })
  }
}

export const searchHistoryStore = new SearchHistoryStore()
