import type { AdminUpdateRoomRequest } from '@/core/api'
import type { RoomListing } from '@/core/realtime'
import { useId, useState } from 'react'
import { api } from '@/core/api'
import { RoomCapacityLimits, RoomDescriptionLimits, RoomNameLimits } from '@/core/realtime'
import { useSubmission } from '@/shared/lib/use-submission'
import { Modal, Select } from '@/shared/ui'
import { adminStore } from './admin-store'
import { capacityRangeError, parseCapacity } from './room-capacity'

type PasswordChange = 'keep' | 'set' | 'remove'

const slugPattern = /^[a-z0-9-]{1,32}$/

// 新建时表单从 defaultCapacity 开始。默认值由服务端给出，前端不另存一份，否则服务端改了默认值，网页上建的房间仍是旧值。
export function RoomFormDialog({ room, defaultCapacity, onSaved, onClose }: { room: RoomListing | null, defaultCapacity: number, onSaved: () => void, onClose: () => void }) {
  const [name, setName] = useState(room?.name ?? '')
  const [description, setDescription] = useState(room?.description ?? '')
  const [slug, setSlug] = useState(room?.slug ?? '')
  const [passwordChange, setPasswordChange] = useState<PasswordChange>(room ? 'keep' : 'set')
  const [password, setPassword] = useState('')
  const [capacityDraft, setCapacityDraft] = useState(String(room?.capacity ?? defaultCapacity))
  const { busy, submit } = useSubmission()
  const slugHintId = useId()
  const capacityHintId = useId()
  const slugInvalid = slug !== '' && !slugPattern.test(slug)
  const capacity = parseCapacity(capacityDraft)
  const save = async () => {
    if (capacity === null)
      return
    const fields = { name: name.trim(), description: description.trim() }
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
          if (capacity !== room.capacity)
            patch.capacity = capacity
          return api.adminUpdateRoom(room.id, patch)
        })
      : await adminStore.call(() => api.adminCreateRoom({ ...fields, capacity, ...(slug ? { slug } : {}), ...(password ? { password } : {}) }))
    if (saved !== null)
      onSaved()
  }
  return (
    <Modal title={room ? '编辑常驻房间' : '新建常驻房间'} onClose={onClose}>
      <form
        className="form-stack"
        onSubmit={(event) => {
          event.preventDefault()
          void submit(save)
        }}
      >
        <label>
          房间名称
          <input data-autofocus value={name} onChange={event => setName(event.target.value)} required maxLength={RoomNameLimits.maxLength} />
        </label>
        <label>
          房间描述
          <textarea value={description} onChange={event => setDescription(event.target.value)} maxLength={RoomDescriptionLimits.maxLength} rows={3} />
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
        <div className="admin-field">
          <label>
            最多人数
            <input type="number" inputMode="numeric" min={RoomCapacityLimits.minimum} max={RoomCapacityLimits.maximum} step={1} value={capacityDraft} onChange={event => setCapacityDraft(event.target.value)} aria-invalid={capacity === null} aria-describedby={capacityHintId} />
          </label>
          <p id={capacityHintId} className={`text-ui-sm ${capacity === null ? 'error-text' : 'muted'}`}>
            {capacity === null ? capacityRangeError : room ? '改了立即生效。调低时不会让已经在房间里的人断开，新来的人要等人数降下来才进得来。' : `${RoomCapacityLimits.minimum} 到 ${RoomCapacityLimits.maximum} 人。`}
          </p>
        </div>
        {room && (
          <label>
            密码
            <Select
              value={passwordChange}
              onChange={setPasswordChange}
              options={[
                { value: 'keep', label: room.hasPassword ? '保持现有密码' : '保持无密码' },
                { value: 'set', label: room.hasPassword ? '修改密码' : '设置密码' },
                ...(room.hasPassword ? [{ value: 'remove' as const, label: '取消密码' }] : []),
              ]}
            />
          </label>
        )}
        {passwordChange === 'set' && (
          <label>
            {room ? '新密码' : '密码（可选）'}
            <input type="password" autoComplete="new-password" value={password} onChange={event => setPassword(event.target.value)} required={room !== null} />
          </label>
        )}
        <button type="submit" className="button-primary" disabled={busy || !name.trim() || slugInvalid || capacity === null}>
          {busy ? '正在保存…' : '保存'}
        </button>
      </form>
    </Modal>
  )
}
