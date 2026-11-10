import type { MyPlaylist } from '@/core/realtime'
import { useState } from 'react'
import { isImeKey } from '@/shared/lib/ime'
import { playlistsStore } from './playlists-store'

export function CreatePlaylistForm({ onCreated, onCancel }: { onCreated: (playlist: MyPlaylist) => void, onCancel: () => void }) {
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
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
      onSubmit={async (event) => {
        event.preventDefault()
        if (saving)
          return
        setSaving(true)
        const playlist = await playlistsStore.create(name)
        if (playlist)
          onCreated(playlist)
        else
          setSaving(false)
      }}
    >
      <label>
        歌单名
        {/* 刚打开的 Modal 按 data-autofocus 聚焦。在已经打开的对话框里切换过来时，由 autoFocus 聚焦。 */}
        <input data-autofocus autoFocus required maxLength={50} value={name} onChange={event => setName(event.target.value)} />
      </label>
      <div className="dialog-actions">
        <button type="button" className="button-secondary" onClick={onCancel}>取消</button>
        <button type="submit" className="button-primary" disabled={saving || name.trim() === ''}>创建</button>
      </div>
    </form>
  )
}
