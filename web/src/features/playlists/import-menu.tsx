import type { MyPlaylist } from '@/core/realtime'
import { FileUp, Import, Link } from 'lucide-react'
import { useRef, useState } from 'react'
import { Modal, notificationStore } from '@/shared/ui'
import { ActionMenu } from './action-menu'
import { importLimit, parsePlaylistFile } from './playlist-file'
import { playlistsStore } from './playlists-store'
import { defaultImportName } from './save-platform-playlist'

async function importFile(file: File): Promise<void> {
  const content = await file.text().catch(() => null)
  const parsed = content === null ? '没能读取这个文件。' : parsePlaylistFile(content)
  if (typeof parsed === 'string') {
    notificationStore.error(parsed)
    return
  }
  const result = await playlistsStore.importFile(parsed.name, parsed.tracks)
  if (!result)
    return
  const done = `已导入 ${result.imported} 首到“${result.playlist.name}”`
  if (parsed.truncated)
    notificationStore.success(`${done}。文件里超过 ${importLimit} 首，只导入了前 ${importLimit} 首。`, 6000)
  else
    notificationStore.success(done)
}

export function ImportMenu({ onOpen }: { onOpen: (playlist: MyPlaylist) => void }) {
  const fileRef = useRef<HTMLInputElement>(null)
  const [importing, setImporting] = useState(false)
  const [linking, setLinking] = useState(false)
  return (
    <>
      <ActionMenu
        actions={[
          { icon: FileUp, label: '从文件导入', run: () => fileRef.current!.click() },
          { icon: Link, label: '从链接导入', run: () => setLinking(true) },
        ]}
        menuLabel="导入歌单"
        className="button-secondary"
        disabled={importing}
      >
        <Import size={18} strokeWidth={1.7} aria-hidden="true" />
        {importing ? '正在导入…' : '导入'}
      </ActionMenu>
      <input
        ref={fileRef}
        type="file"
        accept=".json,application/json"
        hidden
        onChange={async (event) => {
          const file = event.target.files?.[0]
          // 清空之后，再选同一个文件也会触发 change。
          event.target.value = ''
          if (!file)
            return
          setImporting(true)
          await importFile(file)
          setImporting(false)
        }}
      />
      {linking && (
        <ImportLinkDialog
          onClose={() => setLinking(false)}
          onImported={(playlist) => {
            setLinking(false)
            onOpen(playlist)
          }}
        />
      )}
    </>
  )
}

function ImportLinkDialog({ onClose, onImported }: { onClose: () => void, onImported: (playlist: MyPlaylist) => void }) {
  const [link, setLink] = useState('')
  const [name, setName] = useState(defaultImportName)
  const [busy, setBusy] = useState(false)
  return (
    <Modal title="从链接导入" onClose={onClose}>
      <form
        className="form-stack"
        onSubmit={async (event) => {
          event.preventDefault()
          if (busy)
            return
          setBusy(true)
          const found = await playlistsStore.resolveLink(link)
          const result = found && await playlistsStore.importPlatform(name, found.source, found.playlistId)
          if (!result) {
            setBusy(false)
            return
          }
          notificationStore.success(`已导入 ${result.imported} 首`)
          onImported(result.playlist)
        }}
      >
        <label>
          歌单链接
          <input data-autofocus required placeholder="粘贴网易云音乐的歌单链接或分享文字" value={link} onChange={event => setLink(event.target.value)} />
        </label>
        <label>
          歌单名
          <input required maxLength={50} value={name} onChange={event => setName(event.target.value)} />
        </label>
        <div className="dialog-actions">
          <button type="button" className="button-secondary" onClick={onClose}>取消</button>
          <button type="submit" className="button-primary" disabled={busy || link.trim() === '' || name.trim() === ''}>{busy ? '正在导入…' : '导入'}</button>
        </div>
      </form>
    </Modal>
  )
}
