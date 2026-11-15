import type { MyPlaylist } from '@/core/realtime'
import { useState } from 'react'
import { isImeKey } from '@/shared/lib/ime'
import { useSubmission } from '@/shared/lib/use-submission'
import { PlaylistNameField } from './playlist-name-field'
import { isBlankPlaylistName, playlistsStore } from './playlists-store'

export function CreatePlaylistForm({ onCreated, onCancel }: { onCreated: (playlist: MyPlaylist) => void, onCancel: () => void }) {
  const [name, setName] = useState('')
  const { busy, submit } = useSubmission()
  return (
    <form
      className="form-stack"
      onKeyDown={(event) => {
        if (event.key !== 'Escape' || isImeKey(event.nativeEvent))
          return
        // 拦下对话框的 Esc，因为在“保存到歌单”里，Esc 只回到列表。
        event.preventDefault()
        onCancel()
      }}
      onSubmit={(event) => {
        event.preventDefault()
        void submit(async () => {
          const playlist = await playlistsStore.create(name)
          if (playlist)
            onCreated(playlist)
        })
      }}
    >
      {/* 刚打开的 Modal 按 data-autofocus 聚焦。在已经打开的对话框里切换过来时，由 autoFocus 聚焦。 */}
      <PlaylistNameField data-autofocus autoFocus value={name} onChange={setName} />
      <div className="dialog-actions">
        <button type="button" className="button-secondary" onClick={onCancel}>取消</button>
        <button type="submit" className="button-primary" disabled={busy || isBlankPlaylistName(name)}>创建</button>
      </div>
    </form>
  )
}
