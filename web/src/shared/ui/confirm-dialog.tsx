import { useState } from 'react'
import { Modal } from './modal'

export function ConfirmDialog({ title, children, confirmLabel, danger = false, closeOnConfirm = false, onConfirm, onClose, returnFocus }: {
  title: string
  children: string
  confirmLabel: string
  danger?: boolean
  // 为 false 时由 onConfirm 在操作成功之后关闭对话框，失败时对话框留着，可以再试。
  closeOnConfirm?: boolean
  onConfirm: () => Promise<void>
  onClose: () => void
  returnFocus?: () => HTMLElement | null
}) {
  const [busy, setBusy] = useState(false)
  return (
    <Modal title={title} onClose={onClose} returnFocus={returnFocus}>
      <p className="text-ui-base muted">{children}</p>
      <div className="dialog-actions">
        <button type="button" className="button-secondary" onClick={onClose}>取消</button>
        <button
          type="button"
          className={danger ? 'button-destructive' : 'button-primary'}
          disabled={busy}
          aria-busy={busy}
          onClick={async () => {
            setBusy(true)
            try {
              await onConfirm()
            }
            finally {
              setBusy(false)
            }
            if (closeOnConfirm)
              onClose()
          }}
        >
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}
