import type { KeyboardEvent, MouseEvent } from 'react'
import type { MovePlan } from './track-order'
import type { Selection } from './track-selection'
import type { MyPlaylistItem, Track } from '@/core/realtime'
import type { SongAction } from '@/shared/ui'
import { GripVertical } from 'lucide-react'
import { useLayoutEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { sameTrack } from '@/core/room'
import { sourceName } from '@/features/queue'
import { bookTitle } from '@/shared/lib/book-title'
import { formatDuration } from '@/shared/lib/time'
import { AsyncIconButton, PlayingBars, SongArtwork } from '@/shared/ui'
import { trackKey } from './playlists-store'
import { moveByOne } from './track-order'
import { selectAll, selectRange, toggle } from './track-selection'
import { useTrackDrag } from './use-track-drag'

function rowOf(list: HTMLElement | null, key: string): Element | null {
  return list?.querySelector(`[data-key="${CSS.escape(key)}"]`) ?? null
}

// 多选时焦点落在勾选框上，这样读屏软件才读得出这一行是否选中。平时焦点落在行上。
function focusRow(row: Element | null | undefined, selecting: boolean): void {
  const target = selecting ? row?.querySelector('.playlist-track-check') : row
  if (target instanceof HTMLElement)
    target.focus()
}

export function PlaylistTracks({ items, playing, selection, selecting, actions, onSelect, onMove }: {
  items: MyPlaylistItem[]
  playing: Track | undefined
  selection: Selection
  selecting: boolean
  actions: (item: MyPlaylistItem) => SongAction[]
  onSelect: (selection: Selection) => void
  onMove: (plan: MovePlan<string>) => void
}) {
  const listRef = useRef<HTMLOListElement>(null)
  const lineRef = useRef<HTMLDivElement>(null)
  const labelRef = useRef<HTMLDivElement>(null)
  const focusAfterMoveRef = useRef<string | null>(null)
  // 最后获得焦点的行。勾选框随多选出现和消失，焦点在勾选框上时会跟着丢掉。
  const focusedRef = useRef<string | null>(null)
  const keys = items.map(item => trackKey(item.track))
  const { dragging, onPointerDown } = useTrackDrag({
    listRef,
    lineRef,
    labelRef,
    keys,
    selected: selection.keys,
    onDrop: (plan) => {
      if (plan)
        onMove(plan)
    },
  })

  // 重新排列时 React 可能把获得焦点的行移出再插回，焦点会丢。
  useLayoutEffect(() => {
    const key = focusAfterMoveRef.current
    if (key === null)
      return
    focusAfterMoveRef.current = null
    focusRow(rowOf(listRef.current, key), selecting)
  }, [items, selecting])

  // 进出多选时，焦点留在原来那一行，落到这一行在新模式下的位置。
  useLayoutEffect(() => {
    const key = focusedRef.current
    const active = document.activeElement
    if (key === null || (active !== null && active !== document.body && !active.matches('.playlist-track')))
      return
    focusRow(rowOf(listRef.current, key), selecting)
  }, [selecting])

  const onRowClick = (event: MouseEvent, key: string) => {
    if ((event.target as Element).closest('button, a'))
      return
    if (event.shiftKey)
      onSelect(selectRange(selection, keys, key))
    else if (event.metaKey || event.ctrlKey || selecting)
      onSelect(toggle(selection, key))
  }

  const onKeyDown = (event: KeyboardEvent<HTMLOListElement>) => {
    if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'a') {
      event.preventDefault()
      onSelect(selectAll(keys))
      return
    }
    // 其余按键只在焦点落在行本身或它的勾选框上时处理，行里的按钮保持原来的行为。勾选框上的空格由浏览器切换。
    const target = event.target as HTMLElement
    const row = target.closest<HTMLElement>('[data-key]')
    const key = row?.dataset.key
    if (!row || key === undefined || (target !== row && !target.matches('.playlist-track-check')))
      return
    if (event.key === ' ' && selecting && target === row) {
      event.preventDefault()
      onSelect(toggle(selection, key))
      return
    }
    if ((event.key !== 'ArrowUp' && event.key !== 'ArrowDown') || event.metaKey || event.ctrlKey || event.shiftKey)
      return
    event.preventDefault()
    const delta = event.key === 'ArrowUp' ? -1 : 1
    if (!event.altKey) {
      focusRow(delta < 0 ? row.previousElementSibling : row.nextElementSibling, selecting)
      return
    }
    const plan = moveByOne(keys, selection.keys.has(key) ? selection.keys : new Set([key]), delta)
    if (!plan)
      return
    focusAfterMoveRef.current = key
    onMove(plan)
  }

  let label = ''
  if (dragging) {
    const [first] = dragging
    label = dragging.size > 1 ? `${dragging.size} 首` : items.find(item => trackKey(item.track) === first)?.meta.title ?? ''
  }

  return (
    <div className="playlist-tracks" data-dragging={dragging ? true : undefined}>
      <ol ref={listRef} className="song-list" onKeyDown={onKeyDown}>
        {items.map((item, index) => {
          const { track, meta } = item
          const key = keys[index]
          const selected = selection.keys.has(key)
          const source = sourceName(track)
          const isPlaying = sameTrack(track, playing)
          return (
            <li
              key={key}
              data-key={key}
              className="song-row playlist-track"
              tabIndex={selecting ? -1 : 0}
              data-playing={isPlaying || undefined}
              data-selected={selected || undefined}
              data-dragging={dragging?.has(key) || undefined}
              onClick={event => onRowClick(event, key)}
              onFocus={() => {
                focusedRef.current = key
              }}
              onPointerDown={event => onPointerDown(event, key)}
              // 按住封面拖动时，浏览器会开始原生的图片拖拽并取消指针事件，排序的拖动随之中断。
              onDragStart={event => event.preventDefault()}
            >
              {selecting
                ? <input type="checkbox" className="playlist-track-check" checked={selected} readOnly aria-label={`选择${bookTitle(meta.title)}`} />
                : <span className="song-index text-ui-sm muted">{String(index + 1).padStart(2, '0')}</span>}
              {/* 多选时点封面也是切换选中，不打开音源页面。 */}
              <SongArtwork coverUrl={meta.coverUrl} title={meta.title} href={selecting ? undefined : meta.pageUrl} sourceName={source} />
              <div className="song-copy">
                <span className="song-title">
                  {isPlaying && <PlayingBars />}
                  <strong className="text-ui-base truncate">{meta.title}</strong>
                </span>
                <span className="text-ui-sm muted truncate">{[meta.artist || '未知艺术家', source].filter(Boolean).join(' · ')}</span>
              </div>
              <span className="song-album text-ui-sm muted truncate">{meta.album}</span>
              <span className="song-duration text-ui-sm muted">{meta.durationMs > 0 ? formatDuration(meta.durationMs) : ''}</span>
              {actions(item).map(action => (
                <AsyncIconButton key={action.label} icon={action.icon} className={action.secondary ? 'song-action song-action-secondary' : 'song-action'} label={action.label} disabled={action.disabled} onAction={action.run} />
              ))}
              <span className="playlist-track-handle" data-drag-handle aria-hidden="true">
                <GripVertical size={18} strokeWidth={1.7} />
              </span>
            </li>
          )
        })}
      </ol>
      {dragging && <div ref={lineRef} className="playlist-drop-line" />}
      {dragging && createPortal(<div ref={labelRef} className="playlist-drag-label text-ui-sm">{label}</div>, document.body)}
    </div>
  )
}
