import { useEffect } from 'react'
import { profileStore } from '@/core/profile'
import { useStore } from '@/shared/lib/use-store'
import { Skeleton } from '@/shared/ui'
import { openAccountSettings } from './account-settings'

function loadProfile(): void {
  void profileStore.load().catch(() => {})
}

// 调用方在没有账号时显示 AccountPrompt。没有账号有两种情况：是游客，或者资料还没读到。
// 资料没读到时不能提示去注册，否则启动时读取失败的已注册用户会被当成游客。
export function AccountPrompt({ message, onClose }: { message: string, onClose: () => void }) {
  const loaded = useStore(profileStore, s => s.member !== null)
  const failed = useStore(profileStore, s => s.failed)
  useEffect(() => {
    if (!loaded)
      loadProfile()
  }, [loaded])
  if (!loaded && !failed)
    return <Skeleton rows={2} />
  if (!loaded) {
    return (
      <>
        <p className="text-ui-base muted">没能读取你的账号资料。</p>
        <div className="dialog-actions">
          <button type="button" className="button-secondary" onClick={loadProfile}>重试</button>
        </div>
      </>
    )
  }
  return (
    <>
      <p className="text-ui-base muted">{message}</p>
      <div className="dialog-actions">
        <button
          type="button"
          className="button-primary"
          onClick={() => {
            onClose()
            openAccountSettings()
          }}
        >
          去账号设置
        </button>
      </div>
    </>
  )
}
