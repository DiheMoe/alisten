import type { AdminGetRoomApiKeyResponse } from '@/core/api'
import type { RoomListing } from '@/core/realtime'
import { useEffect, useRef, useState } from 'react'
import { api } from '@/core/api'
import { formatDateTime } from '@/shared/lib/time'
import { Modal, notificationStore } from '@/shared/ui'
import { adminStore } from './admin-store'
import { ConfirmDialog } from './confirm-dialog'

// 非安全上下文（比如局域网的明文 HTTP）没有剪贴板接口，用户也可能拒绝授权：这时返回 false，由调用方让用户手动复制。
async function copyApiKey(key: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(key)
    notificationStore.success('已复制 API Key')
    return true
  }
  catch {
    return false
  }
}

export function RoomApiKeyDialog({ room, onClose }: { room: RoomListing, onClose: () => void }) {
  // undefined 表示正在获取，null 表示获取失败。
  const [status, setStatus] = useState<AdminGetRoomApiKeyResponse | null>()
  const [attempt, setAttempt] = useState(0)
  // 只有生成或重置的响应里有 Key，关掉弹窗就再也看不到了。
  const [key, setKey] = useState<string | null>(null)
  const [generating, setGenerating] = useState(false)
  const [resetting, setResetting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    let active = true
    void adminStore.call(() => api.adminGetRoomApiKey(room.id)).then((result) => {
      if (active)
        setStatus(result)
    })
    return () => {
      active = false
    }
  }, [room.id, attempt])
  // 不用 autoFocus：确认弹窗卸载时会把焦点还给打开它之前聚焦的元素（比如这个弹窗的关闭按钮），晚于 autoFocus；这个 effect 在那之后运行。
  useEffect(() => {
    if (key !== null)
      inputRef.current?.focus()
  }, [key])
  const refetch = () => {
    setStatus(undefined)
    setAttempt(n => n + 1)
  }
  // 失败时 Key 可能已经重置，只是没能通知各实例断开旧 Key 的事件流，所以重新获取状态。
  const renew = async (): Promise<string | null> => {
    const result = await adminStore.call(() => api.adminResetRoomApiKey(room.id))
    if (!result)
      refetch()
    return result && result.key
  }
  const generate = async () => {
    setGenerating(true)
    const fresh = await renew()
    setGenerating(false)
    if (fresh) {
      setKey(fresh)
      notificationStore.success('已生成 API Key')
    }
  }
  const copy = async (value: string) => {
    if (await copyApiKey(value))
      return
    inputRef.current?.focus()
    inputRef.current?.select()
    notificationStore.info('没能写入剪贴板，请手动复制选中的 Key。', 8000)
  }
  // 确认弹窗与这个弹窗并列，不放进 Modal 里：React 沿组件树传递 dialog 的 cancel 事件，在确认弹窗里按 Esc 会把两个弹窗一起关掉。
  return (
    <>
      <Modal title={`「${room.name}」的 API Key`} onClose={onClose}>
        {key !== null
          ? (
              <>
                <div className="form-stack">
                  <p className="text-ui-base muted">关闭后不能再查看，请现在复制。</p>
                  <input ref={inputRef} readOnly value={key} aria-label="API Key" onFocus={event => event.currentTarget.select()} />
                </div>
                <div className="dialog-actions">
                  <button type="button" className="button-primary" onClick={() => void copy(key)}>复制</button>
                </div>
              </>
            )
          : (
              <>
                {status === undefined && <p className="text-ui-base muted">正在获取…</p>}
                {status === null && (
                  <div className="admin-failed">
                    <p className="text-ui-caption muted">暂时无法获取 API Key 的状态。</p>
                    <button type="button" className="button-secondary" onClick={refetch}>重试</button>
                  </div>
                )}
                {status?.set === false && (
                  <>
                    <p className="text-ui-base muted">还没有 API Key。</p>
                    <div className="dialog-actions">
                      <button type="button" className="button-primary" disabled={generating} onClick={() => void generate()}>生成</button>
                    </div>
                  </>
                )}
                {status?.set && (
                  <>
                    <p className="text-ui-base muted">{`生成于 ${formatDateTime(status.createdAtMs)}。Key 只在生成时显示一次，丢失后只能重置。`}</p>
                    <div className="dialog-actions">
                      <button type="button" className="button-secondary" onClick={() => setResetting(true)}>重置</button>
                    </div>
                  </>
                )}
              </>
            )}
      </Modal>
      {resetting && (
        <ConfirmDialog
          title="重置 API Key"
          confirmLabel="重置"
          onConfirm={async () => {
            const fresh = await renew()
            if (!fresh)
              return
            setResetting(false)
            setKey(fresh)
            notificationStore.success('已重置 API Key')
          }}
          onClose={() => setResetting(false)}
        >
          重置后旧 Key 立即失效，正在用它的 bot 要换成新 Key。
        </ConfirmDialog>
      )}
    </>
  )
}
