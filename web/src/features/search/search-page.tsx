import type { KeywordQuery, SearchQuery } from './search-store'
import type { PlaylistSummary, TrackSummary } from '@/core/realtime'
import type { SearchCategory, SearchSource } from '@/features/history'
import type { SongItem } from '@/shared/ui'
import { ArrowLeft, ArrowRight, CirclePlus, Clock3, Search, Sparkles } from 'lucide-react'
import { roomStore } from '@/core/room'
import { historyStore } from '@/features/history'
import { enqueue } from '@/features/queue'
import { bookTitle } from '@/shared/lib/book-title'
import { useStore } from '@/shared/lib/use-store'
import { Artwork, AsyncIconButton, EmptyState, IconButton, Skeleton, SongArtwork, SongList } from '@/shared/ui'
import { SearchBox } from './search-box'
import { canOpenPlaylists, keywordSearch, pageSize, recordSearch, searchStore } from './search-store'
import './search.css'

const categories: { view: SearchCategory, label: string }[] = [
  { view: 'all', label: '全部' },
  { view: 'tracks', label: '歌曲' },
  { view: 'playlists', label: '歌单' },
]

const sourceNames: Record<SearchSource, string> = { netease: '网易云音乐', qq: 'QQ 音乐' }

const previewLimit = 4
const playlistPreviewLimit = 5
const recentLimit = 8

function songItem({ track, meta }: TrackSummary): SongItem {
  return {
    key: `${track.source}:${track.id}`,
    title: meta.title,
    artist: meta.artist || '未知艺术家',
    album: meta.album,
    coverUrl: meta.coverUrl,
    durationMs: meta.durationMs,
    sourceUrl: meta.pageUrl,
  }
}

function TrackRows({ items, connected }: { items: TrackSummary[], connected: boolean }) {
  return (
    <SongList
      songs={items.map(songItem)}
      actions={(song, index) => [{
        icon: CirclePlus,
        label: `点歌${bookTitle(song.title)}`,
        disabled: !connected,
        run: async () => {
          await enqueue(items[index].track)
        },
      }]}
    />
  )
}

function TrackTable({ items, connected }: { items: TrackSummary[], connected: boolean }) {
  return (
    <div className="search-song-table">
      <div className="search-song-heading text-ui-caption muted" aria-hidden="true">
        <span>#</span>
        <span>标题</span>
        <span>专辑</span>
        <span><Clock3 size={16} /></span>
        <span />
      </div>
      <TrackRows items={items} connected={connected} />
    </div>
  )
}

function PlaylistGrid({ playlists, connected, onOpen }: { playlists: PlaylistSummary[], connected: boolean, onOpen: (playlist: PlaylistSummary) => void }) {
  return (
    <div className="search-playlist-grid">
      {playlists.map(playlist => (
        <button
          type="button"
          className="search-playlist-card"
          key={`${playlist.source}:${playlist.id}`}
          disabled={!connected}
          onClick={() => onOpen(playlist)}
        >
          <div className="search-playlist-art">
            <Artwork src={playlist.coverUrl} title={playlist.name} />
            <span className="search-playlist-open" aria-hidden="true"><ArrowRight size={20} /></span>
          </div>
          <strong className="text-ui-base truncate" title={playlist.name}>{playlist.name}</strong>
          <span className="text-ui-caption muted truncate">{`${playlist.creator || '未知创建者'} · ${playlist.trackCount} 首歌曲`}</span>
        </button>
      ))}
    </div>
  )
}

function Pagination({ page, total, disabled, onPage }: { page: number, total: number, disabled: boolean, onPage: (page: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return (
    <nav className="pagination" aria-label="翻页">
      <IconButton icon={ArrowLeft} label="上一页" disabled={disabled || page <= 1} onClick={() => onPage(page - 1)} />
      <span className="text-ui-sm muted">{`${page} / ${pages}`}</span>
      <IconButton icon={ArrowRight} label="下一页" disabled={disabled || page >= pages} onClick={() => onPage(page + 1)} />
    </nav>
  )
}

function Overview({ tracks, playlists, connected, onCategory, onOpenPlaylist }: {
  tracks: TrackSummary[]
  playlists: PlaylistSummary[]
  connected: boolean
  onCategory: (category: SearchCategory) => void
  onOpenPlaylist: (playlist: PlaylistSummary) => void
}) {
  const featured = tracks.at(0)
  return (
    <>
      {featured && (
        <div className="search-overview-grid">
          <section aria-labelledby="search-feature-title">
            <h2 id="search-feature-title" className="text-ui-lg search-section-title">最佳匹配</h2>
            <article className="search-featured-song">
              <SongArtwork coverUrl={featured.meta.coverUrl} title={featured.meta.title} href={featured.meta.pageUrl} />
              <h3 className="text-ui-xl truncate" title={featured.meta.title}>{featured.meta.title}</h3>
              <p className="text-ui-caption muted truncate">{featured.meta.artist || '未知艺术家'}</p>
              <div className="search-feature-footer">
                <span className="text-ui-caption">歌曲</span>
                <AsyncIconButton
                  icon={CirclePlus}
                  label={`点歌${bookTitle(featured.meta.title)}`}
                  className="song-action"
                  disabled={!connected}
                  onAction={async () => {
                    await enqueue(featured.track)
                  }}
                />
              </div>
            </article>
          </section>
          <section className="search-preview" aria-labelledby="search-songs-title">
            <div className="section-heading">
              <h2 id="search-songs-title" className="text-ui-lg">歌曲</h2>
              <button type="button" className="search-section-link text-ui-caption" disabled={!connected} onClick={() => onCategory('tracks')}>显示全部</button>
            </div>
            <TrackRows items={tracks.slice(0, previewLimit)} connected={connected} />
          </section>
        </div>
      )}
      {playlists.length > 0 && (
        <section className="search-playlists" aria-labelledby="search-playlists-title">
          <div className="section-heading">
            <h2 id="search-playlists-title" className="text-ui-lg">歌单</h2>
            <button type="button" className="search-section-link text-ui-caption" disabled={!connected} onClick={() => onCategory('playlists')}>显示全部</button>
          </div>
          <PlaylistGrid playlists={playlists.slice(0, playlistPreviewLimit)} connected={connected} onOpen={onOpenPlaylist} />
        </section>
      )}
    </>
  )
}

function RecentSearches({ connected, onSearch }: { connected: boolean, onSearch: (query: SearchQuery) => void }) {
  const searches = useStore(historyStore, s => s.searches)
  const enabled = useStore(historyStore, s => s.searchesEnabled)
  if (!enabled || !searches.length)
    return <EmptyState icon={Search} title="你想听哪首歌？">搜索歌名、歌手或歌单。</EmptyState>
  return (
    <section aria-label="最近搜索">
      <div className="section-heading">
        <h2 className="text-ui-lg">最近搜索</h2>
        <button type="button" className="search-section-link text-ui-caption" onClick={() => historyStore.clearSearches()}>清空</button>
      </div>
      <div className="search-recent-grid">
        {searches.slice(0, recentLimit).map(record => (
          <button
            type="button"
            className="search-recent-card"
            key={record.query}
            disabled={!connected}
            onClick={() => {
              const query = keywordSearch(record.query, record.source, record.category)
              recordSearch(query)
              onSearch(query)
            }}
          >
            <Clock3 size={20} aria-hidden="true" />
            <span className="song-copy">
              <strong className="text-ui-base truncate">{record.query}</strong>
              <span className="text-ui-caption muted">{sourceNames[record.source]}</span>
            </span>
            <ArrowRight size={18} aria-hidden="true" />
          </button>
        ))}
      </div>
    </section>
  )
}

function NoResults({ failed }: { failed: boolean }) {
  return <EmptyState icon={Search} title={failed ? '暂时无法搜索' : '没有找到匹配结果'}>试试另一个歌名、歌手或音乐平台。</EmptyState>
}

function SearchResults({ connected, onSearch, onOpenPlaylist }: { connected: boolean, onSearch: (query: SearchQuery) => void, onOpenPlaylist: (playlist: PlaylistSummary) => void }) {
  const keyword = useStore(searchStore, s => s.keyword)
  const source = useStore(searchStore, s => s.source)
  const view = useStore(searchStore, s => s.view)
  const page = useStore(searchStore, s => s.page)
  const failed = useStore(searchStore, s => s.failed)
  const tracks = useStore(searchStore, s => s.tracks)
  const tracksTotal = useStore(searchStore, s => s.tracksTotal)
  const playlists = useStore(searchStore, s => s.playlists)
  const playlistsTotal = useStore(searchStore, s => s.playlistsTotal)
  const recommendations = useStore(searchStore, s => s.recommendations)
  if (view === 'recommend') {
    return (
      <section aria-label="个性化推荐歌曲">
        <div className="section-heading">
          <h2 className="text-ui-lg">歌曲</h2>
          <button type="button" className="search-section-link text-ui-caption" disabled={!connected} onClick={() => searchStore.retry()}>刷新</button>
        </div>
        {recommendations.length
          ? <TrackTable items={recommendations} connected={connected} />
          : <EmptyState icon={Sparkles} title={failed ? '暂时无法获取推荐' : '暂时没有推荐'}>{failed ? '稍后再来看看。' : '房间里放过网易云音乐的歌之后，再来看看。'}</EmptyState>}
      </section>
    )
  }
  if (!keyword)
    return <RecentSearches connected={connected} onSearch={onSearch} />
  const showCategory = (category: SearchCategory) => {
    const query = keywordSearch(keyword, source, category)
    recordSearch(query)
    onSearch(query)
  }
  if (view === 'all')
    return tracks.length || playlists.length ? <Overview tracks={tracks} playlists={playlists} connected={connected} onCategory={showCategory} onOpenPlaylist={onOpenPlaylist} /> : <NoResults failed={failed} />
  const total = view === 'tracks' ? tracksTotal : playlistsTotal
  const count = view === 'tracks' ? tracks.length : playlists.length
  return (
    <section aria-label={view === 'tracks' ? '歌曲搜索结果' : '歌单搜索结果'}>
      <div className="section-heading">
        <h2 className="text-ui-lg">{view === 'tracks' ? '歌曲' : '歌单'}</h2>
        <span className="text-ui-caption muted">{`${total} 个结果`}</span>
      </div>
      {count
        ? (
            <>
              {view === 'tracks' ? <TrackTable items={tracks} connected={connected} /> : <PlaylistGrid playlists={playlists} connected={connected} onOpen={onOpenPlaylist} />}
              <Pagination page={page} total={total} disabled={!connected} onPage={at => onSearch(keywordSearch(keyword, source, view, at))} />
            </>
          )
        : <NoResults failed={failed} />}
    </section>
  )
}

export function SearchPage({ inlineSearchClassName, onSearch, onOpenPlaylist }: {
  inlineSearchClassName: string
  onSearch: (query: SearchQuery) => void
  onOpenPlaylist: (playlist: PlaylistSummary) => void
}) {
  const keyword = useStore(searchStore, s => s.keyword)
  const source = useStore(searchStore, s => s.source)
  const view = useStore(searchStore, s => s.view)
  const loading = useStore(searchStore, s => s.loading)
  const failed = useStore(searchStore, s => s.failed)
  const connected = useStore(roomStore, s => s.status === 'open')
  const recommending = view === 'recommend'
  const overview = view === 'all'
  const searchAgain = (query: KeywordQuery) => {
    recordSearch(query)
    onSearch(query)
  }
  return (
    <div className="page-content search-page">
      <div className="search-filter-bar">
        <SearchBox className={`search-inline ${inlineSearchClassName}`} recent={false} onSearch={onSearch} />
        <div className="search-filter-chips" role="group" aria-label="搜索类别">
          {categories.filter(category => category.view !== 'playlists' || canOpenPlaylists(source)).map(category => (
            <button
              type="button"
              key={category.view}
              className="search-chip text-ui-caption"
              aria-pressed={view === category.view}
              disabled={!connected}
              onClick={() => searchAgain(keywordSearch(keyword, source, category.view))}
            >
              {category.label}
            </button>
          ))}
          <button type="button" className="search-chip text-ui-caption" aria-pressed={recommending} disabled={!connected} onClick={() => onSearch({ view: 'recommend' })}>
            <Sparkles size={14} aria-hidden="true" />
            个性化推荐
          </button>
        </div>
        {view !== 'recommend' && (
          <select
            className="search-source text-ui-caption"
            aria-label="音乐平台"
            value={source}
            disabled={!connected}
            onChange={event => searchAgain(keywordSearch(keyword, event.target.value === 'qq' ? 'qq' : 'netease', view))}
          >
            <option value="qq">QQ 音乐</option>
            <option value="netease">网易云音乐</option>
          </select>
        )}
      </div>
      <header className="search-page-heading">
        <h1 className="text-ui-xl">{recommending ? '为你推荐' : keyword ? '搜索结果' : '搜索'}</h1>
        <p className="text-ui-caption muted">
          {recommending ? '发现下一首想听的歌。' : keyword ? `“${keyword}” · ${sourceNames[source]}` : '搜索歌名、歌手或歌单。'}
        </p>
        {recommending && (
          <p className="search-recommend-hint text-ui-caption muted">
            <Sparkles size={14} aria-hidden="true" />
            根据房间里放过的歌，推荐相似的歌曲。
          </p>
        )}
      </header>
      {failed && (
        <div className="search-error" role="alert">
          <p className="error-text text-ui-caption">没有全部加载成功。</p>
          <button type="button" className="button-secondary" disabled={!connected || loading} onClick={() => searchStore.retry()}>重试</button>
        </div>
      )}
      {!connected && <p className="text-ui-caption muted">连上房间之后，才能搜索和点歌。</p>}
      {loading
        ? (
            <div className={overview ? 'search-loading-overview' : undefined}>
              {overview && (
                <div className="search-feature-skeleton" aria-hidden="true">
                  <span />
                  <span />
                  <span />
                </div>
              )}
              <Skeleton rows={6} />
            </div>
          )
        : <SearchResults connected={connected} onSearch={onSearch} onOpenPlaylist={onOpenPlaylist} />}
    </div>
  )
}

export function PlaylistPage({ title, page, onPage }: { title: string | undefined, page: number, onPage: (page: number) => void }) {
  const tracks = useStore(searchStore, s => s.tracks)
  const total = useStore(searchStore, s => s.tracksTotal)
  const loading = useStore(searchStore, s => s.loading)
  const failed = useStore(searchStore, s => s.failed)
  const connected = useStore(roomStore, s => s.status === 'open')
  return (
    <div className="page-content search-page">
      <header className="search-page-heading">
        <h1 className="text-ui-xl">{title ?? '歌单'}</h1>
      </header>
      {failed && (
        <div className="search-error" role="alert">
          <p className="error-text text-ui-caption">歌单没有加载成功。</p>
          <button type="button" className="button-secondary" disabled={!connected || loading} onClick={() => searchStore.retry()}>重试</button>
        </div>
      )}
      {!connected && <p className="text-ui-caption muted">连上房间之后，才能打开歌单和点歌。</p>}
      {loading
        ? <Skeleton rows={6} />
        : (
            <section aria-label="歌单内的歌曲">
              <div className="section-heading">
                <h2 className="text-ui-lg">歌曲</h2>
                <span className="text-ui-caption muted">{`${total} 首`}</span>
              </div>
              {tracks.length
                ? (
                    <>
                      <TrackTable items={tracks.slice((page - 1) * pageSize, page * pageSize)} connected={connected} />
                      <Pagination page={page} total={tracks.length} disabled={false} onPage={onPage} />
                    </>
                  )
                : !failed && <EmptyState title="歌单里还没有歌曲" />}
            </section>
          )}
    </div>
  )
}
