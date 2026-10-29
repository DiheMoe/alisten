import { useState } from 'react'
import { Modal } from '@/shared/ui'

export function ConfirmDialog({ title, children, confirmLabel, onConfirm, onClose }: {
  title: string
  children: string
  confirmLabel: string
  onConfirm: () => Promise<void>
  onClose: () => void
}) {
  const [busy, setBusy] = useState(false)
  return (
    <Modal title={title} onClose={onClose}>
      <p className="text-ui-base muted">{children}</p>
      <div className="dialog-actions">
        <button type="button" className="button-secondary" onClick={onClose}>取消</button>
        <button
          type="button"
          className="button-destructive"
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            try {
              await onConfirm()
            }
            finally {
              setBusy(false)
            }
          }}
        >
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}
