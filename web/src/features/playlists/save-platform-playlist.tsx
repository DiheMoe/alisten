import type { Source } from '@/core/realtime'
import { ListPlus } from 'lucide-react'
import { useState } from 'react'
import { profileStore } from '@/core/profile'
import { notificationStore } from '@/shared/lib/notification-store'
import { useStore } from '@/shared/lib/use-store'
import { useSubmission } from '@/shared/lib/use-submission'
import { Modal } from '@/shared/ui'
import { AccountPrompt } from './account-prompt'
import { PlaylistNameField } from './playlist-name-field'
import { isBlankPlaylistName, playlistsStore } from './playlists-store'

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
  const { busy, submit } = useSubmission()
  return (
    <Modal title="保存为我的歌单" onClose={onClose}>
      {account
        ? (
            <form
              className="form-stack"
              onSubmit={(event) => {
                event.preventDefault()
                void submit(async () => {
                  const result = await playlistsStore.importPlatform(name, source, playlistId)
                  if (!result)
                    return
                  notificationStore.success(`已保存为“${result.playlist.name}”，共 ${result.imported} 首`)
                  onClose()
                })
              }}
            >
              <PlaylistNameField data-autofocus value={name} onChange={setName} />
              <div className="dialog-actions">
                <button type="button" className="button-secondary" onClick={onClose}>取消</button>
                <button type="submit" className="button-primary" disabled={busy || isBlankPlaylistName(name)}>{busy ? '正在保存…' : '保存'}</button>
              </div>
            </form>
          )
        : <AccountPrompt message="注册或登录之后就能保存这个歌单。" onClose={onClose} />}
    </Modal>
  )
}
