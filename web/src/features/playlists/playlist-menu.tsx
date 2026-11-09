import type { KeyboardEvent, ToggleEvent } from 'react'
import type { MyPlaylist } from '@/core/realtime'
import { Ellipsis, Pencil, Trash2 } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { ConfirmDialog, Modal } from '@/shared/ui'
import { playlistsStore } from './playlists-store'

const menuGap = 4
const viewportMargin = 8

const menuKeys: Record<string, (index: number, count: number) => number> = {
  ArrowDown: (index, count) => (index + 1) % count,
  ArrowUp: (index, count) => (index - 1 + count) % count,
  Home: () => 0,
  End: (_, count) => count - 1,
}

export function PlaylistMenu({ playlist, align = 'end', className = '', onDeleted }: {
  playlist: MyPlaylist
  align?: 'start' | 'end'
  className?: string
  onDeleted?: () => void
}) {
  const menuId = useId()
  const buttonRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [dialog, setDialog] = useState<'rename' | 'delete' | null>(null)

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

  // 显示之前量不到菜单的尺寸：先贴着按钮下方对齐，显示之后超出视口再翻到按钮上方、收进视口。
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

  const choose = (next: 'rename' | 'delete') => {
    menuRef.current!.hidePopover()
    setDialog(next)
  }

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className={`icon-button playlist-menu-button ${className}`}
        popoverTarget={menuId}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`“${playlist.name}”的更多操作`}
        title="更多操作"
      >
        <Ellipsis size={20} strokeWidth={1.7} aria-hidden="true" />
      </button>
      <div ref={menuRef} id={menuId} className="playlist-menu" popover="auto" role="menu" aria-label={`“${playlist.name}”的操作`} onBeforeToggle={place} onToggle={onToggle} onKeyDown={onKeyDown}>
        <button type="button" role="menuitem" tabIndex={-1} className="playlist-menu-item" onClick={() => choose('rename')}>
          <Pencil size={16} strokeWidth={1.7} aria-hidden="true" />
          重命名
        </button>
        <button type="button" role="menuitem" tabIndex={-1} className="playlist-menu-item playlist-menu-danger" onClick={() => choose('delete')}>
          <Trash2 size={16} strokeWidth={1.7} aria-hidden="true" />
          删除
        </button>
      </div>
      {dialog === 'rename' && <RenameDialog playlist={playlist} onClose={() => setDialog(null)} />}
      {dialog === 'delete' && (
        <ConfirmDialog
          title="删除歌单"
          confirmLabel="删除"
          danger
          onConfirm={async () => {
            if (await playlistsStore.remove(playlist)) {
              setDialog(null)
              onDeleted?.()
            }
          }}
          onClose={() => setDialog(null)}
        >
          {`删除“${playlist.name}”之后，里面的歌也一起删除。`}
        </ConfirmDialog>
      )}
    </>
  )
}

function RenameDialog({ playlist, onClose }: { playlist: MyPlaylist, onClose: () => void }) {
  const [name, setName] = useState(playlist.name)
  const [saving, setSaving] = useState(false)
  return (
    <Modal title="重命名" onClose={onClose}>
      <form
        className="form-stack"
        onSubmit={async (event) => {
          event.preventDefault()
          if (saving)
            return
          setSaving(true)
          if (await playlistsStore.rename(playlist, name))
            onClose()
          else
            setSaving(false)
        }}
      >
        <label>
          歌单名
          <input data-autofocus required maxLength={50} value={name} onChange={event => setName(event.target.value)} />
        </label>
        <button type="submit" className="button-primary" disabled={saving || name.trim() === ''}>保存</button>
      </form>
    </Modal>
  )
}
