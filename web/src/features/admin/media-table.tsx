import type { StoredMedia } from '@/core/realtime'
import { Ellipsis, ExternalLink, Trash2 } from 'lucide-react'
import { bookTitle } from '@/shared/lib/book-title'
import { formatDateTime } from '@/shared/lib/time'
import { ActionMenu, Artwork } from '@/shared/ui'
import { formatPlayed, formatSize, formatStoredDate, mediaByline } from './media-view'

// 按住 Shift 点击会把页面上的文字一起选中，按下时就阻止。
function keepTextSelection(event: { shiftKey: boolean, preventDefault: () => void }): void {
  if (event.shiftKey)
    event.preventDefault()
}

function MediaRow({ item, now, selected, onToggle, onDelete }: {
  item: StoredMedia
  now: number
  selected: boolean
  onToggle: (key: string, shift: boolean) => void
  onDelete: (item: StoredMedia) => void
}) {
  const size = formatSize(item.size)
  const stored = formatStoredDate(item.storedAtMs, now)
  const played = formatPlayed(item.lastPlayedAtMs, now)
  const playedLabel = item.lastPlayedAtMs === null ? played : `最近播放 ${played}`
  return (
    <li className="media-row" data-key={item.key} data-selected={selected || undefined}>
      <label className="media-check" onMouseDown={keepTextSelection}>
        <input
          type="checkbox"
          aria-label={`选择${bookTitle(item.title)}`}
          checked={selected}
          onChange={event => onToggle(item.key, event.nativeEvent instanceof MouseEvent && event.nativeEvent.shiftKey)}
        />
      </label>
      <div className="media-song">
        <Artwork src={item.coverUrl} title={item.title} />
        <div className="media-song-copy">
          <strong className="truncate" title={item.title}>{item.title}</strong>
          <span className="truncate">
            {mediaByline(item)}
            <span className="media-narrow">{` · ${size}`}</span>
          </span>
          <span className="media-narrow truncate">{`存储于 ${stored} · ${playedLabel}`}</span>
        </div>
      </div>
      <span className="media-cell">{size}</span>
      <span className="media-cell" title={formatDateTime(item.storedAtMs)}>
        <span className="sr-only">存储于</span>
        {stored}
      </span>
      <span className="media-cell" title={item.lastPlayedAtMs === null ? undefined : formatDateTime(item.lastPlayedAtMs)}>
        {item.lastPlayedAtMs !== null && <span className="sr-only">最近播放</span>}
        {played}
      </span>
      <ActionMenu
        actions={[
          { icon: ExternalLink, label: '打开歌曲页面', run: () => window.open(item.link, '_blank', 'noopener,noreferrer') },
          { icon: Trash2, label: '删除', danger: true, run: () => onDelete(item) },
        ]}
        menuLabel={`${bookTitle(item.title)}的操作`}
        className="icon-button admin-menu-button"
        label={`${bookTitle(item.title)}的更多操作`}
        title="更多操作"
      >
        <Ellipsis size={20} strokeWidth={1.7} aria-hidden="true" />
      </ActionMenu>
    </li>
  )
}

// busy 时这一页还是换参数之前的内容，不能操作。
export function MediaTable({ items, now, selected, busy, onToggle, onToggleAll, onDelete }: {
  items: StoredMedia[]
  now: number
  selected: ReadonlySet<string>
  busy: boolean
  onToggle: (key: string, shift: boolean) => void
  onToggleAll: () => void
  onDelete: (item: StoredMedia) => void
}) {
  const count = items.filter(item => selected.has(item.key)).length
  return (
    <div className="section-group media-table" aria-busy={busy} inert={busy}>
      <div className="media-head">
        <label className="media-check">
          <input
            type="checkbox"
            aria-label="全选本页"
            checked={count > 0 && count === items.length}
            ref={(input) => {
              if (input)
                input.indeterminate = count > 0 && count < items.length
            }}
            onChange={onToggleAll}
          />
        </label>
        <span aria-hidden="true">歌曲</span>
        <span className="media-cell" aria-hidden="true">大小</span>
        <span className="media-cell" aria-hidden="true">存储于</span>
        <span className="media-cell" aria-hidden="true">最近播放</span>
      </div>
      <ul>
        {items.map(item => <MediaRow key={item.key} item={item} now={now} selected={selected.has(item.key)} onToggle={onToggle} onDelete={onDelete} />)}
      </ul>
    </div>
  )
}
