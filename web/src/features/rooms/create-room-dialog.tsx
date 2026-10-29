import { useState } from 'react'
import { api, saveTicket } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { Modal, notificationStore } from '@/shared/ui'
import { joinErrorMessage, roomEntryStore } from './room-entry'
import './rooms.css'

export function CreateRoomDialog({ onCreated, onClose }: { onCreated: (id: string) => void, onClose: () => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [locked, setLocked] = useState(false)
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const ready = name.trim() !== '' && (!locked || password !== '')
  const submit = async () => {
    setBusy(true)
    const secret = locked ? { password } : {}
    let id: string
    try {
      id = (await api.createRoom({ name: name.trim(), description: description.trim(), ...secret })).id
    }
    catch (error) {
      notificationStore.error(errorMessage(error))
      setBusy(false)
      return
    }
    notificationStore.success('已创建房间')
    try {
      saveTicket(id, (await api.createTicket(id, secret)).ticket)
    }
    catch (error) {
      // 房间已经创建：进入后由加入弹窗重新换取票据，留在此处重试会再创建一个房间。
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
          void submit()
        }}
      >
        <label>
          房间名称
          <input data-autofocus value={name} onChange={event => setName(event.target.value)} required maxLength={80} placeholder="给这个房间起个名字" />
        </label>
        <label>
          房间描述
          <textarea value={description} onChange={event => setDescription(event.target.value)} maxLength={500} rows={3} placeholder="你想和大家听什么？" />
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
        <button type="submit" className="button-primary" disabled={busy || !ready}>
          {busy ? '正在创建…' : '创建并加入'}
        </button>
      </form>
    </Modal>
  )
}
