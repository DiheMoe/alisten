import type { Source } from '@/core/realtime'
import { ListPlus } from 'lucide-react'
import { useState } from 'react'
import { profileStore } from '@/features/settings'
import { useStore } from '@/shared/lib/use-store'
import { Modal, notificationStore } from '@/shared/ui'
import { AccountPrompt } from './account-prompt'
import { playlistsStore } from './playlists-store'

export const defaultImportName = '导入的歌单'

export function SavePlatformPlaylistButton({ source, playlistId, name }: { source: Source, playlistId: string, name?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className="button-secondary" onClick={() => setOpen(true)}>
        <ListPlus size={18} strokeWidth={1.7} aria-hidden="true" />
        保存为我的歌单
      </button>
      {open && <SavePlatformPlaylistDialog source={source} playlistId={playlistId} name={name ?? defaultImportName} onClose={() => setOpen(false)} />}
    </>
  )
}

function SavePlatformPlaylistDialog({ source, playlistId, name: original, onClose }: { source: Source, playlistId: string, name: string, onClose: () => void }) {
  const account = useStore(profileStore, s => s.account)
  const [name, setName] = useState(original)
  const [busy, setBusy] = useState(false)
  return (
    <Modal title="保存为我的歌单" onClose={onClose}>
      {account
        ? (
            <form
              className="form-stack"
              onSubmit={async (event) => {
                event.preventDefault()
                if (busy)
                  return
                setBusy(true)
                const result = await playlistsStore.importPlatform(name, source, playlistId)
                if (!result) {
                  setBusy(false)
                  return
                }
                notificationStore.success(`已保存为“${result.playlist.name}”，共 ${result.imported} 首`)
                onClose()
              }}
            >
              <label>
                歌单名
                <input data-autofocus required maxLength={50} value={name} onChange={event => setName(event.target.value)} />
              </label>
              <div className="dialog-actions">
                <button type="button" className="button-secondary" onClick={onClose}>取消</button>
                <button type="submit" className="button-primary" disabled={busy || name.trim() === ''}>{busy ? '正在保存…' : '保存'}</button>
              </div>
            </form>
          )
        : <AccountPrompt message="注册或登录之后就能保存这个歌单。" onClose={onClose} />}
    </Modal>
  )
}
