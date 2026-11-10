import type { MyPlaylist } from '@/core/realtime'
import type { MenuAction } from '@/shared/ui'
import { Copy, Ellipsis, FileDown, Link2Off, Pencil, RefreshCw, Share2, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { ActionMenu, ConfirmDialog, Modal, notificationStore } from '@/shared/ui'
import { playlistsStore } from './playlists-store'

async function copyShareLink(shareId: string, copied: string): Promise<void> {
  const url = `${window.location.origin}/s/${shareId}`
  try {
    await navigator.clipboard.writeText(url)
    notificationStore.success(copied)
  }
  catch {
    // 非安全上下文没有剪贴板接口，用户也可能拒绝授权；有的浏览器在请求返回之后已不算用户操作，也会拒绝写入。
    notificationStore.info(`请手动复制分享链接：${url}`, 8000)
  }
}

async function share(playlist: MyPlaylist, copied: string): Promise<void> {
  const shareId = await playlistsStore.share(playlist)
  if (shareId)
    await copyShareLink(shareId, copied)
}

export function PlaylistMenu({ playlist, align = 'end', className = '', shareable = false, onDeleted, onExport }: {
  playlist: MyPlaylist
  align?: 'start' | 'end'
  className?: string
  shareable?: boolean
  onDeleted?: () => void
  onExport?: () => void
}) {
  const [dialog, setDialog] = useState<'rename' | 'delete' | 'reshare' | 'unshare' | null>(null)
  const actions: MenuAction[] = []
  if (!playlist.favorites)
    actions.push({ icon: Pencil, label: '重命名', run: () => setDialog('rename') })
  if (shareable && playlist.shareId) {
    const { shareId } = playlist
    actions.push(
      { icon: Copy, label: '复制分享链接', run: () => void copyShareLink(shareId, '已复制分享链接') },
      { icon: RefreshCw, label: '重新生成分享链接', run: () => setDialog('reshare') },
      { icon: Link2Off, label: '停止分享', run: () => setDialog('unshare') },
    )
  }
  else if (shareable) {
    actions.push({ icon: Share2, label: '分享', run: () => void share(playlist, '已开启分享，链接已复制') })
  }
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
      {dialog === 'reshare' && (
        <ConfirmDialog
          title="重新生成分享链接"
          confirmLabel="重新生成"
          closeOnConfirm
          onConfirm={() => share(playlist, '已生成新链接并复制')}
          onClose={() => setDialog(null)}
        >
          生成新链接之后，之前分享出去的链接就打不开了。
        </ConfirmDialog>
      )}
      {dialog === 'unshare' && (
        <ConfirmDialog
          title="停止分享"
          confirmLabel="停止分享"
          danger
          closeOnConfirm
          onConfirm={async () => {
            if (await playlistsStore.unshare(playlist))
              notificationStore.success('已停止分享')
          }}
          onClose={() => setDialog(null)}
        >
          停止之后，分享出去的链接就打不开了。
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
