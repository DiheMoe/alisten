import type { MenuAction } from './action-menu'
import type { MyPlaylist } from '@/core/realtime'
import { Ellipsis, FileDown, Pencil, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { ConfirmDialog, Modal } from '@/shared/ui'
import { ActionMenu } from './action-menu'
import { playlistsStore } from './playlists-store'

export function PlaylistMenu({ playlist, align = 'end', className = '', onDeleted, onExport }: {
  playlist: MyPlaylist
  align?: 'start' | 'end'
  className?: string
  onDeleted?: () => void
  onExport?: () => void
}) {
  const [dialog, setDialog] = useState<'rename' | 'delete' | null>(null)
  const actions: MenuAction[] = []
  if (!playlist.favorites)
    actions.push({ icon: Pencil, label: '重命名', run: () => setDialog('rename') })
  if (onExport)
    actions.push({ icon: FileDown, label: '导出', run: onExport })
  if (!playlist.favorites)
    actions.push({ icon: Trash2, label: '删除', danger: true, run: () => setDialog('delete') })
  return (
    <>
      <ActionMenu
        actions={actions}
        menuLabel={`“${playlist.name}”的操作`}
        align={align}
        className={`icon-button playlist-menu-button ${className}`}
        label={`“${playlist.name}”的更多操作`}
        title="更多操作"
      >
        <Ellipsis size={20} strokeWidth={1.7} aria-hidden="true" />
      </ActionMenu>
      {dialog === 'rename' && <RenameDialog playlist={playlist} onClose={() => setDialog(null)} />}
      {dialog === 'delete' && (
        <ConfirmDialog
          title="删除歌单"
          confirmLabel="删除"
          danger
          onConfirm={async () => {
            if (await playlistsStore.remove(playlist)) {
              setDialog(null)
              onDeleted?.()
            }
          }}
          onClose={() => setDialog(null)}
        >
          {`删除“${playlist.name}”之后，里面的歌也一起删除。`}
        </ConfirmDialog>
      )}
    </>
  )
}

function RenameDialog({ playlist, onClose }: { playlist: MyPlaylist, onClose: () => void }) {
  const [name, setName] = useState(playlist.name)
  const [saving, setSaving] = useState(false)
  return (
    <Modal title="重命名" onClose={onClose}>
      <form
        className="form-stack"
        onSubmit={async (event) => {
          event.preventDefault()
          if (saving)
            return
          setSaving(true)
          if (await playlistsStore.rename(playlist, name))
            onClose()
          else
            setSaving(false)
        }}
      >
        <label>
          歌单名
          <input data-autofocus required maxLength={50} value={name} onChange={event => setName(event.target.value)} />
        </label>
        <button type="submit" className="button-primary" disabled={saving || name.trim() === ''}>保存</button>
      </form>
    </Modal>
  )
}
