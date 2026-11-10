import type { AdminListMediaResponse } from '@/core/api'
import type { StoredMedia as Media } from '@/core/realtime'
import { Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '@/core/api'
import { bookTitle } from '@/shared/lib/book-title'
import { formatDateTime } from '@/shared/lib/time'
import { ConfirmDialog, IconButton, notificationStore, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'
import { LoadFailed, SectionHead } from './section-head'

function formatSize(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
}

export function StoredMedia() {
  const [media, setMedia] = useState<AdminListMediaResponse | null>(null)
  const [failed, setFailed] = useState(false)
  const [reload, setReload] = useState(0)
  const [deleting, setDeleting] = useState<Media | null>(null)
  useEffect(() => {
    let active = true
    void adminStore.call(() => api.adminListMedia({ page: 1 })).then((result) => {
      if (!active)
        return
      if (result)
        setMedia(result)
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
  return (
    <section className="admin-section" aria-labelledby="admin-media-title">
      <SectionHead id="admin-media-title" title="媒体" description="服务器上缓存的音频。删除后，下次有人点这首歌时会重新下载。">
        {media && <span className="text-ui-sm muted">{media.total ? `${media.total} 首 · ${formatSize(media.totalBytes)}` : '0 首'}</span>}
      </SectionHead>
      {failed
        ? <LoadFailed message="暂时无法获取已存储的媒体。" onRetry={retry} />
        : !media
            ? <Skeleton rows={3} />
            : !media.items.length
                ? <p className="admin-empty text-ui-caption muted">还没有存储任何歌曲。</p>
                : (
                    <ul className="admin-list">
                      {media.items.map(item => (
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
          danger
          onConfirm={async () => {
            const result = await adminStore.call(() => api.adminRemoveMedia({ media: [{ key: deleting.key }] }))
            if (result === null)
              return
            if (result.inUse.length) {
              notificationStore.error('这首歌还在房间的播放列表中，不能删除')
              return
            }
            setDeleting(null)
            setFailed(false)
            setReload(n => n + 1)
          }}
          onClose={() => setDeleting(null)}
        >
          {`删除${bookTitle(deleting.title)}的音频和封面后，再点这首歌会重新下载。`}
        </ConfirmDialog>
      )}
    </section>
  )
}
