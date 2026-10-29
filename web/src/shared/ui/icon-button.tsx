import type { LucideIcon } from 'lucide-react'
import type { ButtonHTMLAttributes } from 'react'
import { Ellipsis } from 'lucide-react'
import { useRef, useState } from 'react'

interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  icon: LucideIcon
  label: string
}

export function IconButton({ icon: Icon, label, className = '', ...props }: IconButtonProps) {
  return (
    <button type="button" className={`icon-button ${className}`} aria-label={label} title={label} {...props}>
      <Icon size={20} strokeWidth={1.7} aria-hidden="true" />
    </button>
  )
}

export function AsyncIconButton({ onAction, disabled, icon, ...props }: Omit<IconButtonProps, 'onClick'> & { onAction: () => Promise<void> }) {
  const [pending, setPending] = useState(false)
  // state 要到下一次渲染才更新，连续点击时由这个 ref 阻止重复提交。
  const pendingRef = useRef(false)
  return (
    <IconButton
      {...props}
      icon={pending ? Ellipsis : icon}
      disabled={disabled || pending}
      aria-busy={pending}
      onClick={async () => {
        if (pendingRef.current)
          return
        pendingRef.current = true
        setPending(true)
        try {
          await onAction()
        }
        finally {
          pendingRef.current = false
          setPending(false)
        }
      }}
    />
  )
}
