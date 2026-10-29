import type { PlayRecord } from './records'
import { CirclePlus, Download, History, Search, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { roomStore } from '@/core/room'
import { enqueue, sourceName } from '@/features/queue'
import { bookTitle } from '@/shared/lib/book-title'
import { formatClock } from '@/shared/lib/time'
import { useStore } from '@/shared/lib/use-store'
import { AsyncIconButton, EmptyState, IconButton, Modal, notificationStore, SongArtwork } from '@/shared/ui'
import { historyStore } from './history-store'
import './history.css'

const batchSize = 80

function matches(record: PlayRecord, query: string): boolean {
  const keyword = query.trim().toLowerCase()
  const { title, artist, album } = record.meta
  return !keyword || `${title} ${artist} ${album}`.toLowerCase().includes(keyword)
}

function playedOn(ms: number): string {
  return new Date(ms).toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' })
}

function HistoryRow({ record, compact, connected }: { record: PlayRecord, compact: boolean, connected: boolean }) {
  const { title, artist, album, coverUrl, pageUrl } = record.meta
  const performer = artist || '未知艺术家'
  const source = sourceName(record.track)
  return (
    <div className="song-row">
      <SongArtwork coverUrl={coverUrl} title={title} href={pageUrl} sourceName={source} />
      <div className="song-copy">
        <strong className="text-ui-base truncate">{title}</strong>
        <span className="text-ui-sm muted truncate">{[performer, source, album].filter(Boolean).join(' · ')}</span>
      </div>
      {!compact && <time className="history-time text-ui-sm muted" dateTime={new Date(record.playedAtMs).toISOString()}>{formatClock(record.playedAtMs)}</time>}
      <AsyncIconButton
        icon={CirclePlus}
        className="song-action"
        label={`点歌${bookTitle(title)}`}
        disabled={!connected}
        onAction={async () => {
          await enqueue(record.track)
        }}
      />
      <IconButton
        icon={Trash2}
        className="song-action"
        label={`删除${bookTitle(title)}的播放记录`}
        onClick={() => {
          historyStore.removePlay(record)
          notificationStore.success('已删除播放记录')
        }}
      />
    </div>
  )
}

export function HistoryPage({ compact = false, filter = '', onFilter }: { compact?: boolean, filter?: string, onFilter?: (filter: string) => void }) {
  const plays = useStore(historyStore, s => s.plays)
  const connected = useStore(roomStore, s => s.status === 'open')
  const [limit, setLimit] = useState(batchSize)
  const [confirming, setConfirming] = useState(false)
  const visible = plays.filter(record => matches(record, filter))
  const shown = visible.slice(0, limit)
  return (
    <section className={compact ? 'history-panel' : 'page-content history-page'} aria-label="播放历史">
      {!compact && (
        <>
          <div className="page-title">
            <h1 className="text-ui-xl">播放历史</h1>
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
            <IconButton icon={Download} label="导出播放历史" disabled={!plays.length} onClick={() => historyStore.exportPlays()} />
            <IconButton icon={Trash2} label="清空播放历史" disabled={!plays.length} onClick={() => setConfirming(true)} />
          </div>
          <div className="section-heading">
            <span className="text-ui-sm muted">{`${plays.length} 首歌曲 · 保存在这台设备上`}</span>
          </div>
        </>
      )}
      {shown.length
        ? (
            <ol className="history-list">
              {shown.map((record, index) => {
                const day = playedOn(record.playedAtMs)
                return (
                  <li key={`${record.track.source}:${record.track.id}`}>
                    {(index === 0 || playedOn(shown[index - 1].playedAtMs) !== day) && <h2 className="history-date text-ui-sm muted">{day}</h2>}
                    <HistoryRow record={record} compact={compact} connected={connected} />
                  </li>
                )
              })}
            </ol>
          )
        : (
            <EmptyState icon={History} title={filter ? '没有找到相关记录' : '你的音乐足迹，从这里开始'}>
              {filter ? '换个关键词，再找找看。' : '房间播放过的歌曲会保存在这台设备上，方便再次点歌。'}
            </EmptyState>
          )}
      {visible.length > limit && (
        <button type="button" className="button-secondary load-more" onClick={() => setLimit(n => n + batchSize)}>显示更多</button>
      )}
      {confirming && (
        <Modal title="清空播放历史" onClose={() => setConfirming(false)}>
          <p className="text-ui-base muted">将清空这台设备上的全部播放记录。</p>
          <div className="dialog-actions">
            <button type="button" className="button-secondary" onClick={() => setConfirming(false)}>取消</button>
            <button
              type="button"
              className="button-destructive"
              onClick={() => {
                historyStore.clearPlays()
                setConfirming(false)
              }}
            >
              清空
            </button>
          </div>
        </Modal>
      )}
    </section>
  )
}
