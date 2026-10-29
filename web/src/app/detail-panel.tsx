import type { ReactNode } from 'react'
import type { Panel } from './route'
import { useEffect, useRef, useState } from 'react'
import { canPushViews } from './view-transition'

export function DetailPanel({ view, children }: { view: Panel | null, children: (view: Panel) => ReactNode }) {
  const [shown, setShown] = useState(view)
  const [lastView, setLastView] = useState(view)
  const [swapped, setSwapped] = useState(false)
  const panelRef = useRef<HTMLElement>(null)
  if (view !== lastView) {
    setLastView(view)
    setSwapped(view !== null && lastView !== null)
    if (view)
      setShown(view)
  }
  const open = view !== null
  useEffect(() => {
    if (open || !shown || !panelRef.current)
      return
    const duration = Number.parseFloat(getComputedStyle(panelRef.current).animationDuration) * 1000
    // 动画被取消或面板被断点隐藏时不会触发 animationend，由定时器兜底卸载。
    const timer = setTimeout(setShown, duration ? duration + 50 : 0, null)
    return () => clearTimeout(timer)
  }, [open, shown])
  if (!shown)
    return null
  return (
    <aside
      ref={panelRef}
      className={`detail-panel${open ? '' : ' detail-panel-closing'}`}
      aria-hidden={!open || undefined}
      inert={!open}
      onAnimationEnd={(event) => {
        if (event.target === event.currentTarget && !open)
          setShown(null)
      }}
    >
      <div className={`detail-panel-content${swapped && !canPushViews() ? ' detail-panel-swap' : ''}`} key={shown}>{children(shown)}</div>
    </aside>
  )
}
