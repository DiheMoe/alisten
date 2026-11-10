import type { ReactNode } from 'react'
import { X } from 'lucide-react'
import { useEffect, useEffectEvent, useId, useRef } from 'react'
import { createPortal } from 'react-dom'
import { registerModalHistory } from '@/shared/lib/session-history'
import { IconButton } from './icon-button'

export function Modal({ title, children, onClose, returnFocus }: {
  title: string
  children: ReactNode
  onClose: () => void
  // 关闭时焦点回到打开前的元素；它已经不能获得焦点（比如触发对话框的那一行被删掉了、按钮被禁用了）时，落到 returnFocus 返回的元素上。
  returnFocus?: () => HTMLElement | null
}) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  const close = useEffectEvent(onClose)
  const fallbackFocus = useEffectEvent(() => returnFocus?.() ?? null)
  useEffect(() => {
    const dialog = dialogRef.current!
    const previousFocus = document.activeElement as HTMLElement | null
    dialog.showModal()
    // React 不渲染 autofocus 属性，showModal() 会把焦点放在第一个可聚焦的元素上，也就是关闭按钮。
    dialog.querySelector<HTMLElement>('[data-autofocus]')?.focus()
    const unregister = registerModalHistory(() => close())
    return () => {
      unregister()
      dialog.close()
      previousFocus?.focus()
      if (document.activeElement !== previousFocus)
        fallbackFocus()?.focus()
    }
  }, [])
  return createPortal(
    <dialog
      ref={dialogRef}
      className="modal"
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault()
        onClose()
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget)
          onClose()
      }}
    >
      <div className="modal-inner">
        <header className="modal-header">
          <h2 id={titleId} className="text-ui-lg">{title}</h2>
          <IconButton icon={X} label="关闭" onClick={onClose} />
        </header>
        <div className="modal-body">{children}</div>
      </div>
    </dialog>,
    document.body,
  )
}
