import { useStore } from '@/shared/lib/use-store'
import { Modal } from '@/shared/ui'
import { pwaStore } from './pwa'

export function UpdatePrompt() {
  const needRefresh = useStore(pwaStore, s => s.needRefresh)
  if (!needRefresh)
    return null
  return (
    <Modal title="新版本已就绪" onClose={() => pwaStore.dismiss()}>
      <p className="text-ui-base muted">更新后会重新加载应用，并重新连上房间。</p>
      <div className="dialog-actions">
        <button type="button" className="button-secondary" onClick={() => pwaStore.dismiss()}>稍后再说</button>
        <button type="button" className="button-primary" onClick={() => pwaStore.update()}>立即更新</button>
      </div>
    </Modal>
  )
}
