import { useState } from 'react'
import { api, saveTicket } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { RoomDescriptionLimits, RoomNameLimits } from '@/core/realtime'
import { notificationStore } from '@/shared/lib/notification-store'
import { useResource } from '@/shared/lib/use-resource'
import { useSubmission } from '@/shared/lib/use-submission'
import { Modal } from '@/shared/ui'
import { joinErrorMessage, roomEntryStore } from './room-entry'
import './rooms.css'

export function CreateRoomDialog({ onCreated, onClose }: { onCreated: (id: string) => void, onClose: () => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [locked, setLocked] = useState(false)
  const [password, setPassword] = useState('')
  const { busy, submit } = useSubmission()
  const site = useResource(() => api.getSite())
  const ready = name.trim() !== '' && (!locked || password !== '')
  const create = async () => {
    const secret = locked ? { password } : {}
    let id: string
    try {
      id = (await api.createRoom({ name: name.trim(), description: description.trim(), ...secret })).id
    }
    catch (error) {
      notificationStore.error(errorMessage(error))
      return
    }
    notificationStore.success('已创建房间')
    try {
      saveTicket(id, (await api.createTicket(id, secret)).ticket)
    }
    catch (error) {
      // 房间已经创建，进入后由加入弹窗重新换取票据。留在此处重试的话，会再创建一个房间。
      notificationStore.error(joinErrorMessage(error))
    }
    void roomEntryStore.enter(id)
    onCreated(id)
  }
  return (
    <Modal title="创建房间" onClose={onClose}>
      <form
        className="form-stack join-form"
        onSubmit={(event) => {
          event.preventDefault()
          void submit(create)
        }}
      >
        <label>
          房间名称
          <input data-autofocus value={name} onChange={event => setName(event.target.value)} required maxLength={RoomNameLimits.maxLength} />
        </label>
        <label>
          房间描述
          <textarea value={description} onChange={event => setDescription(event.target.value)} maxLength={RoomDescriptionLimits.maxLength} rows={3} placeholder="选填" />
        </label>
        <label className="checkbox-label">
          <input type="checkbox" checked={locked} onChange={event => setLocked(event.target.checked)} />
          设置房间密码
        </label>
        {locked && (
          <label>
            房间密码
            <input type="password" autoComplete="new-password" value={password} onChange={event => setPassword(event.target.value)} required />
          </label>
        )}
        {site.state.status === 'loaded' && <p className="text-ui-caption muted">{`房间最多 ${site.state.value.ephemeralRoomCapacity} 人。`}</p>}
        <button type="submit" className="button-primary" disabled={busy || !ready}>
          {busy ? '正在创建…' : '创建并加入'}
        </button>
      </form>
    </Modal>
  )
}
