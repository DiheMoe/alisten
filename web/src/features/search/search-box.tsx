import type { KeywordQuery, SearchQuery } from './search-store'
import { Clock, Link, Search, X } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { roomStore } from '@/core/room'
import { historyStore } from '@/features/history'
import { enqueue } from '@/features/queue'
import { useStore } from '@/shared/lib/use-store'
import { IconButton } from '@/shared/ui'
import { pickableLink } from './link'
import { keywordSearch, recordSearch, searchStore } from './search-store'
import './search.css'

const recentLimit = 4

export function SearchBox({ className, recent, onSearch }: { className: string, recent: boolean, onSearch: (query: SearchQuery) => void }) {
  const text = useStore(searchStore, s => s.text)
  const connected = useStore(roomStore, s => s.status === 'open')
  const kind = useStore(roomStore, s => s.room?.room.kind)
  const searches = useStore(historyStore, s => s.searches)
  const searchesEnabled = useStore(historyStore, s => s.searchesEnabled)
  const [open, setOpen] = useState(false)
  const [linking, setLinking] = useState(false)
  const boxRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const popoverId = useId()
  const link = pickableLink(text, kind)
  const recentSearches = recent && searchesEnabled ? searches.slice(0, recentLimit) : []
  const popover = open && (link !== null || recentSearches.length > 0)
  useEffect(() => {
    if (!open)
      return
    const dismiss = (event: PointerEvent) => {
      if (!boxRef.current?.contains(event.target as Node))
        setOpen(false)
    }
    document.addEventListener('pointerdown', dismiss)
    return () => document.removeEventListener('pointerdown', dismiss)
  }, [open])
  const search = (query: KeywordQuery) => {
    // 不在房间里时不会搜索，也就不记入搜索历史。
    if (roomStore.state.roomId !== null)
      recordSearch(query)
    setOpen(false)
    onSearch(query)
  }
  return (
    <div
      className={`search-box ${className}`}
      ref={boxRef}
      onKeyDown={(event) => {
        if (event.key === 'Escape')
          setOpen(false)
      }}
    >
      <form
        className={`search-box-field${popover ? ' search-open' : ''}`}
        role="search"
        onSubmit={(event) => {
          event.preventDefault()
          const keyword = text.trim()
          if (keyword)
            search(searchStore.withKeyword(keyword))
        }}
      >
        <Search size={22} strokeWidth={1.7} aria-hidden="true" />
        <input
          ref={inputRef}
          aria-label="搜索音乐"
          aria-haspopup="dialog"
          aria-expanded={popover}
          aria-controls={popover ? popoverId : undefined}
          placeholder="想播放什么？"
          maxLength={500}
          value={text}
          onFocus={() => setOpen(true)}
          onClick={() => setOpen(true)}
          onChange={(event) => {
            searchStore.setText(event.target.value)
            // 回车或 Esc 关闭浮窗后焦点仍在输入框里，不会再触发 focus 和 click，所以输入时重新打开。
            setOpen(true)
          }}
        />
        {text && (
          <IconButton
            icon={X}
            label="清空搜索"
            onClick={() => {
              searchStore.setText('')
              onSearch(searchStore.withKeyword(''))
              // 清空按钮随文字一起卸载，需要把焦点移回输入框。
              inputRef.current?.focus()
            }}
          />
        )}
      </form>
      {popover && (
        <div className="search-popover" role="dialog" aria-label="搜索选项" id={popoverId}>
          {link && (
            <button
              type="button"
              className="search-popover-item search-link text-ui-caption"
              disabled={!connected || linking}
              aria-busy={linking}
              onClick={async () => {
                setLinking(true)
                const added = await enqueue({ source: 'link', id: link })
                setLinking(false)
                // 请求期间输入框仍可编辑：文字未变时才清空，期间输入的内容要保留。
                if (added && searchStore.state.text === text) {
                  searchStore.setText('')
                  setOpen(false)
                }
              }}
            >
              <Link size={16} aria-hidden="true" />
              <span className="search-popover-copy">
                <strong className="text-ui-base">{linking ? '正在点歌…' : '点这个链接'}</strong>
                <span className="muted truncate">{link}</span>
              </span>
            </button>
          )}
          {recentSearches.length > 0 && (
            <div className="search-recent-list">
              {recentSearches.map(record => (
                <button
                  type="button"
                  className="search-popover-item text-ui-caption"
                  key={record.query}
                  disabled={!connected}
                  onClick={() => search(keywordSearch(record.query, record.source, record.category))}
                >
                  <Clock size={16} aria-hidden="true" />
                  <span className="truncate">{record.query}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
