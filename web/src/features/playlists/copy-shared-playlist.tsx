import { CopyPlus } from 'lucide-react'
import { useState } from 'react'
import { profileStore } from '@/features/settings'
import { useStore } from '@/shared/lib/use-store'
import { Modal, notificationStore } from '@/shared/ui'
import { AccountPrompt } from './account-prompt'
import { playlistsStore } from './playlists-store'

export function CopySharedPlaylistButton({ shareId, name }: { shareId: string, name: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className="button-primary" onClick={() => setOpen(true)}>
        <CopyPlus size={18} strokeWidth={1.7} aria-hidden="true" />
        复制到我的歌单
      </button>
      {open && <CopySharedPlaylistDialog shareId={shareId} name={name} onClose={() => setOpen(false)} />}
    </>
  )
}

function CopySharedPlaylistDialog({ shareId, name: original, onClose }: { shareId: string, name: string, onClose: () => void }) {
  const account = useStore(profileStore, s => s.account)
  const [name, setName] = useState(original)
  const [busy, setBusy] = useState(false)
  return (
    <Modal title="复制到我的歌单" onClose={onClose}>
      {account
        ? (
            <form
              className="form-stack"
              onSubmit={async (event) => {
                event.preventDefault()
                if (busy)
                  return
                setBusy(true)
                const playlist = await playlistsStore.copyShared(shareId, name)
                if (!playlist) {
                  setBusy(false)
                  return
                }
                notificationStore.success(`已复制为“${playlist.name}”，共 ${playlist.trackCount} 首`)
                onClose()
              }}
            >
              <label>
                歌单名
                <input data-autofocus required maxLength={50} value={name} onChange={event => setName(event.target.value)} />
              </label>
              <div className="dialog-actions">
                <button type="button" className="button-secondary" onClick={onClose}>取消</button>
                <button type="submit" className="button-primary" disabled={busy || name.trim() === ''}>{busy ? '正在复制…' : '复制'}</button>
              </div>
            </form>
          )
        : <AccountPrompt message="注册或登录之后就能把这个歌单复制到自己的歌单。" onClose={onClose} />}
    </Modal>
  )
}
