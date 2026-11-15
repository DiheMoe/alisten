import { CopyPlus } from 'lucide-react'
import { useState } from 'react'
import { profileStore } from '@/core/profile'
import { notificationStore } from '@/shared/lib/notification-store'
import { useStore } from '@/shared/lib/use-store'
import { useSubmission } from '@/shared/lib/use-submission'
import { Modal } from '@/shared/ui'
import { AccountPrompt } from './account-prompt'
import { PlaylistNameField } from './playlist-name-field'
import { isBlankPlaylistName, playlistsStore } from './playlists-store'

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
  const { busy, submit } = useSubmission()
  return (
    <Modal title="复制到我的歌单" onClose={onClose}>
      {account
        ? (
            <form
              className="form-stack"
              onSubmit={(event) => {
                event.preventDefault()
                void submit(async () => {
                  const playlist = await playlistsStore.copyShared(shareId, name)
                  if (!playlist)
                    return
                  notificationStore.success(`已复制为“${playlist.name}”，共 ${playlist.trackCount} 首`)
                  onClose()
                })
              }}
            >
              <PlaylistNameField data-autofocus value={name} onChange={setName} />
              <div className="dialog-actions">
                <button type="button" className="button-secondary" onClick={onClose}>取消</button>
                <button type="submit" className="button-primary" disabled={busy || isBlankPlaylistName(name)}>{busy ? '正在复制…' : '复制'}</button>
              </div>
            </form>
          )
        : <AccountPrompt message="注册或登录之后就能把这个歌单复制到自己的歌单。" onClose={onClose} />}
    </Modal>
  )
}
