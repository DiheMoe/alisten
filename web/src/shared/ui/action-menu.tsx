import type { LucideIcon } from 'lucide-react'
import type { KeyboardEvent, ReactNode, ToggleEvent } from 'react'
import { useEffect, useId, useRef, useState } from 'react'

const menuGap = 4
const viewportMargin = 8

const menuKeys: Record<string, (index: number, count: number) => number> = {
  ArrowDown: (index, count) => (index + 1) % count,
  ArrowUp: (index, count) => (index - 1 + count) % count,
  Home: () => 0,
  End: (_, count) => count - 1,
}

export interface MenuAction {
  icon: LucideIcon
  label: string
  danger?: boolean
  run: () => void
}

export function ActionMenu({ actions, menuLabel, align = 'end', className, label, title, disabled, children }: {
  actions: MenuAction[]
  menuLabel: string
  align?: 'start' | 'end'
  className: string
  label?: string
  title?: string
  disabled?: boolean
  children: ReactNode
}) {
  const menuId = useId()
  const buttonRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)

  // 菜单在顶层按视口定位，不跟随页面滚动，所以滚动和缩放窗口时收起。
  useEffect(() => {
    if (!open)
      return
    const close = () => menuRef.current?.hidePopover()
    document.addEventListener('scroll', close, true)
    window.addEventListener('resize', close)
    return () => {
      document.removeEventListener('scroll', close, true)
      window.removeEventListener('resize', close)
    }
  }, [open])

  const items = () => [...menuRef.current!.querySelectorAll<HTMLElement>('[role="menuitem"]')]

  // 显示之前量不到菜单的尺寸，所以先贴着按钮下方对齐。显示之后如果超出视口，再翻到按钮上方、收进视口。
  const place = (event: ToggleEvent<HTMLDivElement>) => {
    if (event.newState !== 'open')
      return
    const button = buttonRef.current!.getBoundingClientRect()
    const { style } = event.currentTarget
    style.top = `${button.bottom + menuGap}px`
    style.left = align === 'start' ? `${button.left}px` : 'auto'
    style.right = align === 'end' ? `${document.documentElement.clientWidth - button.right}px` : 'auto'
  }

  const onToggle = (event: ToggleEvent<HTMLDivElement>) => {
    const opened = event.newState === 'open'
    setOpen(opened)
    if (!opened)
      return
    const menu = event.currentTarget
    const button = buttonRef.current!.getBoundingClientRect()
    const rect = menu.getBoundingClientRect()
    if (rect.bottom > window.innerHeight - viewportMargin)
      menu.style.top = `${Math.max(viewportMargin, button.top - menuGap - rect.height)}px`
    const left = Math.max(viewportMargin, Math.min(rect.left, document.documentElement.clientWidth - viewportMargin - rect.width))
    if (left !== rect.left) {
      menu.style.left = `${left}px`
      menu.style.right = 'auto'
    }
    items()[0]?.focus()
  }

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Tab') {
      event.currentTarget.hidePopover()
      return
    }
    const move = menuKeys[event.key]
    if (!move)
      return
    event.preventDefault()
    const list = items()
    list[move(list.indexOf(document.activeElement as HTMLElement), list.length)]?.focus()
  }

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className={className}
        popoverTarget={menuId}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        title={title}
        disabled={disabled}
      >
        {children}
      </button>
      <div ref={menuRef} id={menuId} className="action-menu" popover="auto" role="menu" aria-label={menuLabel} onBeforeToggle={place} onToggle={onToggle} onKeyDown={onKeyDown}>
        {actions.map(({ icon: Icon, label, danger, run }) => (
          <button
            key={label}
            type="button"
            role="menuitem"
            tabIndex={-1}
            className={danger ? 'action-menu-item action-menu-danger' : 'action-menu-item'}
            onClick={() => {
              menuRef.current!.hidePopover()
              run()
            }}
          >
            <Icon size={16} strokeWidth={1.7} aria-hidden="true" />
            {label}
          </button>
        ))}
      </div>
    </>
  )
}
