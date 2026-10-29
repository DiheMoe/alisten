import { useStore } from '@/shared/lib/use-store'
import { Modal } from '@/shared/ui'
import { playerStore } from './player-store'

export function ManualStartPrompt() {
  const needsGesture = useStore(playerStore, s => s.needsGesture)
  if (!needsGesture)
    return null
  const later = () => playerStore.dismissGesture()
  return (
    <Modal title="音乐已就绪" onClose={later}>
      <p className="text-ui-base muted">浏览器需要你点击一次，才能开始播放音乐。</p>
      <div className="dialog-actions">
        <button type="button" className="button-secondary" onClick={later}>稍后播放</button>
        <button type="button" className="button-primary" onClick={() => playerStore.resume()}>点击开始播放</button>
      </div>
    </Modal>
  )
}
