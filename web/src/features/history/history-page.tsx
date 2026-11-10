import type { MyListen } from '@/core/realtime'
import { CirclePlus, Download, History, Search, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { roomStore, sameTrack } from '@/core/room'
import { SaveToPlaylistButton } from '@/features/playlists'
import { enqueue, sourceName } from '@/features/queue'
import { bookTitle } from '@/shared/lib/book-title'
import { formatClock } from '@/shared/lib/time'
import { useStore } from '@/shared/lib/use-store'
import { AsyncIconButton, ConfirmDialog, EmptyState, IconButton, notificationStore, PlayingBars, Skeleton, SongArtwork } from '@/shared/ui'
import { historyStore } from './history-store'
import './history.css'

const batchSize = 80

function matches(listen: MyListen, query: string): boolean {
  const keyword = query.trim().toLowerCase()
  const { title, artist, album } = listen.meta
  return !keyword || `${title} ${artist} ${album}`.toLowerCase().includes(keyword)
}

function playedOn(ms: number): string {
  return new Date(ms).toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' })
}

function HistoryRow({ listen, compact, connected, playing }: { listen: MyListen, compact: boolean, connected: boolean, playing: boolean }) {
  const { title, artist, album, coverUrl, pageUrl } = listen.meta
  const performer = artist || '未知艺术家'
  const source = sourceName(listen.track)
  return (
    <div className="song-row" data-playing={playing || undefined}>
      <SongArtwork coverUrl={coverUrl} title={title} href={pageUrl} sourceName={source} />
      <div className="song-copy">
        <span className="song-title">
          {playing && <PlayingBars />}
          <strong className="text-ui-base truncate">{title}</strong>
        </span>
        <span className="text-ui-sm muted truncate">{[performer, source, album].filter(Boolean).join(' · ')}</span>
      </div>
      {!compact && <time className="history-time text-ui-sm muted" dateTime={new Date(listen.heardAtMs).toISOString()}>{formatClock(listen.heardAtMs)}</time>}
      <AsyncIconButton
        icon={CirclePlus}
        className="song-action"
        label={`点歌${bookTitle(title)}`}
        disabled={!connected}
        onAction={async () => {
          await enqueue(listen.track)
        }}
      />
      <SaveToPlaylistButton track={listen.track} title={title} className="song-action song-action-secondary" />
      <AsyncIconButton
        icon={Trash2}
        className="song-action song-action-secondary"
        label={`删除${bookTitle(title)}的播放记录`}
        onAction={async () => {
          if (await historyStore.remove(listen))
            notificationStore.success('已删除播放记录')
        }}
      />
    </div>
  )
}

export function HistoryPage({ compact = false, filter = '', onFilter }: { compact?: boolean, filter?: string, onFilter?: (filter: string) => void }) {
  const listens = useStore(historyStore, s => s.listens)
  const error = useStore(historyStore, s => s.error)
  const connected = useStore(roomStore, s => s.status === 'open')
  const playing = useStore(roomStore, s => s.room?.nowPlaying?.entry.track)
  const [limit, setLimit] = useState(batchSize)
  const [confirming, setConfirming] = useState(false)
  const titleRef = useRef<HTMLHeadingElement>(null)
  useEffect(() => historyStore.open(), [])
  const all = listens ?? []
  const visible = all.filter(listen => matches(listen, filter))
  const shown = visible.slice(0, limit)
  let body
  if (listens === null) {
    body = error === null
      ? <Skeleton rows={4} />
      : (
          <EmptyState icon={History} title="播放历史没有读出来" action={<button type="button" className="button-secondary" onClick={() => void historyStore.load()}>重试</button>}>
            {error}
          </EmptyState>
        )
  }
  else if (shown.length) {
    body = (
      <ol className="history-list">
        {shown.map((listen, index) => {
          const day = playedOn(listen.heardAtMs)
          return (
            <li key={`${listen.track.source}:${listen.track.id}`}>
              {(index === 0 || playedOn(shown[index - 1].heardAtMs) !== day) && <h2 className="history-date text-ui-sm muted">{day}</h2>}
              <HistoryRow listen={listen} compact={compact} connected={connected} playing={sameTrack(listen.track, playing)} />
            </li>
          )
        })}
      </ol>
    )
  }
  else {
    body = (
      <EmptyState icon={History} title={filter ? '没有找到相关记录' : '你的音乐足迹，从这里开始'}>
        {filter ? '换个关键词，再找找看。' : '你在房间里听过的歌会记在这里。'}
      </EmptyState>
    )
  }
  return (
    <section className={compact ? 'history-panel' : 'page-content history-page'} aria-label="播放历史">
      {!compact && (
        <>
          <div className="page-title">
            <h1 ref={titleRef} className="text-ui-xl" tabIndex={-1}>播放历史</h1>
            <p className="text-ui-base muted">重温那些一起听过的旋律。</p>
          </div>
          <div className="history-toolbar">
            <label className="search-field">
              <Search size={18} aria-hidden="true" />
              <input
                value={filter}
                onChange={(event) => {
                  onFilter?.(event.target.value)
                  setLimit(batchSize)
                }}
                placeholder="搜索播放历史"
                aria-label="搜索播放历史"
              />
            </label>
            <IconButton icon={Download} label="导出播放历史" disabled={!all.length} onClick={() => historyStore.exportListens()} />
            <IconButton icon={Trash2} label="清空播放历史" disabled={!all.length} onClick={() => setConfirming(true)} />
          </div>
          {listens && (
            <div className="section-heading">
              <span className="text-ui-sm muted">{`${listens.length} 首歌曲`}</span>
            </div>
          )}
        </>
      )}
      {body}
      {visible.length > limit && (
        <button type="button" className="button-secondary load-more" onClick={() => setLimit(n => n + batchSize)}>显示更多</button>
      )}
      {confirming && (
        <ConfirmDialog
          title="清空播放历史"
          confirmLabel="清空"
          danger
          onConfirm={async () => {
            if (await historyStore.clear())
              setConfirming(false)
          }}
          onClose={() => setConfirming(false)}
          returnFocus={() => titleRef.current}
        >
          将清空你的全部播放记录，登录同一个账号的其他设备上也会一起清空。
        </ConfirmDialog>
      )}
    </section>
  )
}
