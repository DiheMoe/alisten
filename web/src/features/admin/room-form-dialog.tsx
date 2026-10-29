import type { AdminUpdateRoomRequest } from '@/core/api'
import type { RoomListing } from '@/core/realtime'
import { useId, useState } from 'react'
import { api } from '@/core/api'
import { Modal } from '@/shared/ui'
import { adminStore } from './admin-store'

type PasswordChange = 'keep' | 'set' | 'remove'

const slugPattern = /^[a-z0-9-]{1,32}$/

export function RoomFormDialog({ room, onSaved, onClose }: { room: RoomListing | null, onSaved: () => void, onClose: () => void }) {
  const [name, setName] = useState(room?.name ?? '')
  const [description, setDescription] = useState(room?.description ?? '')
  const [slug, setSlug] = useState(room?.slug ?? '')
  const [passwordChange, setPasswordChange] = useState<PasswordChange>(room ? 'keep' : 'set')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const slugHintId = useId()
  const slugInvalid = slug !== '' && !slugPattern.test(slug)
  const save = async () => {
    setBusy(true)
    const fields = { name: name.trim(), description: description.trim() }
    try {
      const saved = room
        ? await adminStore.call(() => {
            const patch: AdminUpdateRoomRequest = { ...fields }
            // slug 或 password 为空字符串表示取消别名或密码。
            if (slug !== (room.slug ?? ''))
              patch.slug = slug
            if (passwordChange === 'set')
              patch.password = password
            if (passwordChange === 'remove')
              patch.password = ''
            return api.adminUpdateRoom(room.id, patch)
          })
        : await adminStore.call(() => api.adminCreateRoom({ ...fields, ...(slug ? { slug } : {}), ...(password ? { password } : {}) }))
      if (saved !== null)
        onSaved()
    }
    finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={room ? '编辑常驻房间' : '新建常驻房间'} onClose={onClose}>
      <form
        className="form-stack"
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <label>
          房间名称
          <input data-autofocus value={name} onChange={event => setName(event.target.value)} required maxLength={80} />
        </label>
        <label>
          房间描述
          <textarea value={description} onChange={event => setDescription(event.target.value)} maxLength={500} rows={3} />
        </label>
        <div className="admin-field">
          <label>
            别名
            <input value={slug} onChange={event => setSlug(event.target.value)} maxLength={32} placeholder="小写字母、数字和连字符，留空表示不设别名" aria-invalid={slugInvalid} aria-describedby={slugHintId} />
          </label>
          <p id={slugHintId} className={`text-ui-sm ${slugInvalid ? 'error-text' : 'muted'}`}>
            {slugInvalid ? '只能使用小写字母、数字和连字符，最多 32 个字符。' : slug ? `邀请链接：${window.location.origin}/r/${slug}` : '没有别名时，邀请链接使用房间 ID。'}
          </p>
        </div>
        {room && (
          <label>
            密码
            <select value={passwordChange} onChange={event => setPasswordChange(event.target.value as PasswordChange)}>
              <option value="keep">{room.hasPassword ? '保持现有密码' : '保持无密码'}</option>
              <option value="set">{room.hasPassword ? '修改密码' : '设置密码'}</option>
              {room.hasPassword && <option value="remove">取消密码</option>}
            </select>
          </label>
        )}
        {passwordChange === 'set' && (
          <label>
            {room ? '新密码' : '密码（可选）'}
            <input type="password" autoComplete="new-password" value={password} onChange={event => setPassword(event.target.value)} required={room !== null} />
          </label>
        )}
        <button type="submit" className="button-primary" disabled={busy || !name.trim() || slugInvalid}>
          {busy ? '正在保存…' : '保存'}
        </button>
      </form>
    </Modal>
  )
}
