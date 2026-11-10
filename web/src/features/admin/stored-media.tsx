import type { MediaQuery } from './media-store'
import type { MediaSort, StoredMedia } from '@/core/realtime'
import type { SelectOption } from '@/shared/ui'
import { Database, Search, SearchX } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { rowAfterRemoval } from '@/shared/lib/removal-focus'
import { useStore } from '@/shared/lib/use-store'
import { ConfirmDialog, EmptyState, notificationStore, Pagination, Select, Skeleton } from '@/shared/ui'
import { fallbackPage, MediaStore } from './media-store'
import { MediaTable } from './media-table'
import { deleteConfirmText, formatSize, removalMessage, searchDraft } from './media-view'
import { rowOrHeading } from './removal-focus'
import { LoadFailed, SectionHead } from './section-head'

const searchDelayMs = 300
// 与接口对 q 的长度限制一致。
const searchMaxLength = 100

const sortOptions: (SelectOption & { value: MediaSort })[] = [
  { value: 'stored', label: '最近存储' },
  { value: 'idle', label: '最久未播放' },
  { value: 'size', label: '占用最大' },
]

// 输入停顿之后才改写地址，输入法组字期间不算停顿：不拿拼音字母去搜索。
function MediaSearch({ q, onSearch }: { q: string, onSearch: (q: string) => void }) {
  const [draft, setDraft] = useState(q)
  const [seenQ, setSeenQ] = useState(q)
  const [composing, setComposing] = useState(false)
  const search = useEffectEvent(onSearch)
  if (q !== seenQ) {
    setSeenQ(q)
    setDraft(searchDraft(draft, q))
  }
  useEffect(() => {
    const next = draft.trim()
    if (composing || next === q)
      return
    const timer = setTimeout(search, searchDelayMs, next)
    return () => clearTimeout(timer)
  }, [draft, q, composing])
  return (
    <label className="admin-search">
      <Search size={16} aria-hidden="true" />
      <input
        type="search"
        aria-label="搜索标题"
        placeholder="搜索标题"
        maxLength={searchMaxLength}
        value={draft}
        onChange={event => setDraft(event.target.value)}
        onCompositionStart={() => setComposing(true)}
        onCompositionEnd={() => setComposing(false)}
      />
    </label>
  )
}

export function MediaPage({ q, sort, page, onNavigate }: MediaQuery & { onNavigate: (query: MediaQuery) => void }) {
  const [store] = useState(() => new MediaStore())
  const list = useStore(store, s => s.list)
  const loadedAt = useStore(store, s => s.loadedAt)
  const loading = useStore(store, s => s.loading)
  const error = useStore(store, s => s.error)
  const selected = useStore(store, s => s.selected)
  const [deleting, setDeleting] = useState<StoredMedia[] | null>(null)
  // 删掉的歌之后焦点落到哪一行：null 表示落到标题。
  const nextFocusRef = useRef<string | null>(null)

  useEffect(() => store.show({ q, sort, page }), [store, q, sort, page])

  const fallback = list && !loading && error === null ? fallbackPage(list) : null
  const navigate = useEffectEvent(onNavigate)
  useEffect(() => {
    if (fallback !== null)
      navigate({ q, sort, page: fallback })
  }, [fallback, q, sort])

  // 页码在表格下面，换页之后回到顶部。用 nearest：start 会把外层不该滚动的页面框架也一起滚上去。
  const goToPage = (next: number) => {
    onNavigate({ q, sort, page: next })
    document.getElementById('admin-media-title')?.scrollIntoView({ block: 'nearest' })
  }
  const chosen = list?.items.filter(item => selected.has(item.key)) ?? []

  const content = () => {
    if (error !== null)
      return <LoadFailed message={`暂时无法获取已存储的媒体。${error}`} onRetry={() => store.retry()} />
    if (!list || fallback !== null)
      return <Skeleton rows={3} />
    if (!list.items.length) {
      return q
        ? (
            <EmptyState icon={SearchX} title="没有找到歌曲" action={<button type="button" className="button-secondary" onClick={() => onNavigate({ q: '', sort, page: 1 })}>清除搜索</button>}>
              换个标题再找找。
            </EmptyState>
          )
        : <EmptyState icon={Database} title="还没有存储任何歌曲">点过的链接和哔哩哔哩的歌会存在这里。</EmptyState>
    }
    const first = (list.page - 1) * list.pageSize + 1
    return (
      <>
        <MediaTable
          items={list.items}
          now={loadedAt}
          selected={selected}
          busy={loading}
          onToggle={(key, shift) => store.toggle(key, shift)}
          onToggleAll={() => store.toggleAll()}
          onDelete={item => setDeleting([item])}
        />
        <Pagination page={page} totalPages={Math.ceil(list.total / list.pageSize)} onPage={goToPage} summary={`第 ${first}–${first + list.items.length - 1} 首`} />
      </>
    )
  }

  return (
    <section className="admin-section" aria-labelledby="admin-media-title">
      <SectionHead id="admin-media-title" title="媒体" description="服务器上缓存的音频。删除后，下次有人点这首歌时会重新下载。" />
      {chosen.length > 0 && (
        <div className="media-selection-anchor">
          <div className="media-selection" role="toolbar" aria-label="选中的歌曲">
            <span className="media-selection-count">{`已选 ${chosen.length} 首 · ${formatSize(chosen.reduce((sum, item) => sum + item.size, 0))}`}</span>
            <button type="button" className="button-destructive" onClick={() => setDeleting(chosen)}>删除</button>
            <button type="button" className="button-ghost" onClick={() => store.clearSelection()}>取消</button>
          </div>
        </div>
      )}
      <div className="media-toolbar" data-hidden={chosen.length > 0 || undefined}>
        <MediaSearch q={q} onSearch={next => onNavigate({ q: next, sort, page: 1 })} />
        <Select className="media-sort" ariaLabel="排序" value={sort} options={sortOptions} onChange={next => onNavigate({ q, sort: next as MediaSort, page: 1 })} />
        {list && list.total > 0 && <span className="media-total">{`共 ${list.total.toLocaleString('zh-CN')} 首 · 占用 ${formatSize(list.totalBytes)}`}</span>}
      </div>
      {content()}
      {deleting && (
        <ConfirmDialog
          title="删除已存储的媒体"
          confirmLabel={deleting.length > 1 ? `删除 ${deleting.length} 首` : '删除'}
          danger
          onConfirm={async () => {
            const before = list?.items.map(item => item.key) ?? []
            const keys = deleting.map(item => item.key)
            const result = await store.remove(keys)
            if (!result)
              return
            nextFocusRef.current = rowAfterRemoval(before, store.state.list?.items.map(item => item.key) ?? [], keys)
            setDeleting(null)
            const message = removalMessage(deleting.length, result.inUse.length)
            if (result.inUse.length)
              notificationStore.info(message, 5000)
            else
              notificationStore.success(message)
          }}
          onClose={() => setDeleting(null)}
          returnFocus={() => rowOrHeading('admin-media-title', nextFocusRef.current)}
        >
          {deleteConfirmText(deleting)}
        </ConfirmDialog>
      )}
    </section>
  )
}
