import type { StoredMedia as Media } from '@/core/realtime'
import { Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { bookTitle } from '@/shared/lib/book-title'
import { formatDateTime } from '@/shared/lib/time'
import { IconButton, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'
import { ConfirmDialog } from './confirm-dialog'

function formatSize(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
}

export function StoredMedia() {
  const [media, setMedia] = useState<Media[] | null>(null)
  const [failed, setFailed] = useState(false)
  const [reload, setReload] = useState(0)
  const [deleting, setDeleting] = useState<Media | null>(null)
  useEffect(() => {
    let active = true
    void adminStore.call(() => api.adminListMedia()).then((result) => {
      if (!active)
        return
      if (result)
        setMedia(result.items)
      else
        setFailed(true)
    })
    return () => {
      active = false
    }
  }, [reload])
  const retry = () => {
    setFailed(false)
    setMedia(null)
    setReload(n => n + 1)
  }
  const totalSize = media?.reduce((sum, item) => sum + item.size, 0) ?? 0
  return (
    <section className="admin-section" aria-labelledby="admin-media-title">
      <div className="section-heading">
        <h2 id="admin-media-title" className="text-ui-lg">已存储的媒体</h2>
        {media && <span className="text-ui-sm muted">{media.length ? `${media.length} 首 · ${formatSize(totalSize)}` : '0 首'}</span>}
      </div>
      {failed
        ? (
            <div className="admin-empty admin-failed">
              <p className="text-ui-caption muted">暂时无法获取已存储的媒体。</p>
              <button type="button" className="button-secondary" onClick={retry}>重试</button>
            </div>
          )
        : !media
            ? <Skeleton rows={3} />
            : !media.length
                ? <p className="admin-empty text-ui-caption muted">还没有存储任何歌曲。</p>
                : (
                    <ul className="admin-list">
                      {media.map(item => (
                        <li className="admin-row" key={item.key}>
                          <div className="admin-row-copy">
                            <strong className="text-ui-base truncate">{item.title}</strong>
                            <a className="admin-link text-ui-sm truncate" href={item.link} target="_blank" rel="noopener noreferrer">{item.link}</a>
                            <span className="text-ui-xs muted">
                              {`${item.source === 'bilibili' ? '哔哩哔哩 · ' : ''}${formatSize(item.size)} · 存入于 ${formatDateTime(item.storedAtMs)} · ${item.lastPlayedAtMs === null ? '还没有播放过' : `最近播放于 ${formatDateTime(item.lastPlayedAtMs)}`}`}
                            </span>
                          </div>
                          <IconButton icon={Trash2} label={`删除${bookTitle(item.title)}`} onClick={() => setDeleting(item)} />
                        </li>
                      ))}
                    </ul>
                  )}
      {deleting && (
        <ConfirmDialog
          title="删除已存储的媒体"
          confirmLabel="删除"
          onConfirm={async () => {
            if (await adminStore.call(() => api.adminDeleteMedia(deleting.key)) !== null) {
              setDeleting(null)
              setFailed(false)
              setReload(n => n + 1)
            }
          }}
          onClose={() => setDeleting(null)}
        >
          {`删除${bookTitle(deleting.title)}的音频和封面后，再点这首歌会重新下载。`}
        </ConfirmDialog>
      )}
    </section>
  )
}
