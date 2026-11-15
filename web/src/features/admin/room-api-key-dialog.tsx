import type { RoomListing } from '@/core/realtime'
import { useEffect, useRef, useState } from 'react'
import { api } from '@/core/api'
import { copyText } from '@/shared/lib/clipboard'
import { notificationStore } from '@/shared/lib/notification-store'
import { formatDateTime } from '@/shared/lib/time'
import { useResource } from '@/shared/lib/use-resource'
import { ConfirmDialog, Modal } from '@/shared/ui'
import { adminStore } from './admin-store'

async function copyApiKey(key: string): Promise<boolean> {
  const copied = await copyText(key)
  if (copied)
    notificationStore.success('已复制 API Key')
  return copied
}

export function RoomApiKeyDialog({ room, onClose }: { room: RoomListing, onClose: () => void }) {
  const { state: keyStatus, reload: refetch } = useResource(async () => {
    const result = await adminStore.call(() => api.adminGetRoomApiKey(room.id))
    if (result === null)
      throw new Error('暂时无法获取 API Key 的状态。')
    return result
  }, room.id)
  // 只有生成或重置的响应里有 Key，关掉弹窗就再也看不到了。
  const [key, setKey] = useState<string | null>(null)
  const [generating, setGenerating] = useState(false)
  const [resetting, setResetting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  // 不用 autoFocus，因为确认弹窗卸载时会把焦点还给打开它之前聚焦的元素（比如这个弹窗的关闭按钮），时间晚于 autoFocus。这个 effect 在还回焦点之后才运行。
  useEffect(() => {
    if (key !== null)
      inputRef.current?.focus()
  }, [key])
  // 请求失败时服务端仍可能已经重置了 Key（比如响应没有送达），所以重新获取状态。
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
  // 确认弹窗与这个弹窗并列，不放进 Modal 里。React 沿组件树传递 dialog 的 cancel 事件，放进去的话，在确认弹窗里按 Esc 会把两个弹窗一起关掉。
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
                {keyStatus.status === 'loading' && <p className="text-ui-base muted">正在获取…</p>}
                {keyStatus.status === 'failed' && (
                  <div className="admin-failed">
                    <p className="text-ui-caption muted">{keyStatus.error}</p>
                    <button type="button" className="button-secondary" onClick={refetch}>重试</button>
                  </div>
                )}
                {keyStatus.status === 'loaded' && !keyStatus.value.set && (
                  <>
                    <p className="text-ui-base muted">还没有 API Key。</p>
                    <div className="dialog-actions">
                      <button type="button" className="button-primary" disabled={generating} onClick={() => void generate()}>生成</button>
                    </div>
                  </>
                )}
                {keyStatus.status === 'loaded' && keyStatus.value.set && (
                  <>
                    <p className="text-ui-base muted">{`生成于 ${formatDateTime(keyStatus.value.createdAtMs)}。Key 只在生成时显示一次，丢失后只能重置。`}</p>
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
          danger
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
