import type { PointerEvent as ReactPointerEvent, RefObject } from 'react'
import type { MovePlan } from './track-order'
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { dropAt } from './track-order'

// 按下之后移动超过这个距离才算拖动，否则是点击。
const dragThreshold = 4
const scrollZone = 56
const maxScrollStep = 18

function scrollParent(element: HTMLElement): HTMLElement {
  for (let node = element.parentElement; node; node = node.parentElement) {
    const { overflowY } = getComputedStyle(node)
    if (overflowY === 'auto' || overflowY === 'scroll')
      return node
  }
  return document.documentElement
}

// 插入位置：指针在第几行的上半部分之上，取 0 到行数。
function insertionIndex(rows: readonly HTMLElement[], y: number): number {
  let low = 0
  let high = rows.length
  while (low < high) {
    const middle = (low + high) >> 1
    const rect = rows[middle].getBoundingClientRect()
    if (y < rect.top + rect.height / 2)
      high = middle
    else
      low = middle + 1
  }
  return low
}

// 拖动结束时，按下和松开的位置不在同一行，浏览器仍会派发 click：吞掉它，免得切换选中。
function swallowNextClick(): void {
  const swallow = (event: MouseEvent) => {
    event.stopPropagation()
    event.preventDefault()
  }
  window.addEventListener('click', swallow, { capture: true, once: true })
  setTimeout(() => window.removeEventListener('click', swallow, { capture: true }), 0)
}

// 鼠标按住行的任意位置都能拖，触屏只能拖把手，行的其余部分留给滚动。
// 插入线和指针旁的标签每一帧直接改样式，不经过 React，拖动时整张列表不会重新渲染。
export function useTrackDrag({ listRef, lineRef, labelRef, keys, selected, onDrop }: {
  listRef: RefObject<HTMLElement | null>
  lineRef: RefObject<HTMLElement | null>
  labelRef: RefObject<HTMLElement | null>
  keys: readonly string[]
  selected: ReadonlySet<string>
  onDrop: (plan: MovePlan<string> | null) => void
}) {
  const [dragging, setDragging] = useState<ReadonlySet<string> | null>(null)
  const latestRef = useRef({ keys, selected, onDrop })
  useLayoutEffect(() => {
    latestRef.current = { keys, selected, onDrop }
  })
  const stopRef = useRef<(() => void) | null>(null)
  useEffect(() => () => stopRef.current?.(), [])

  const onPointerDown = (event: ReactPointerEvent<HTMLElement>, key: string) => {
    const target = event.target as Element
    if (event.button !== 0 || stopRef.current || target.closest('button, a'))
      return
    if (event.pointerType !== 'mouse' && !target.closest('[data-drag-handle]'))
      return
    const list = listRef.current
    if (!list)
      return
    const { pointerId } = event
    const scroller = scrollParent(list)
    const start = { x: event.clientX, y: event.clientY }
    const pointer = { ...start }
    let moved: ReadonlySet<string> | null = null
    let frame = 0

    const rows = () => [...list.querySelectorAll<HTMLElement>('[data-key]')]

    const autoScroll = () => {
      const rect = scroller === document.documentElement ? new DOMRect(0, 0, innerWidth, innerHeight) : scroller.getBoundingClientRect()
      const top = Math.max(rect.top, 0)
      const bottom = Math.min(rect.bottom, innerHeight)
      const into = pointer.y < top + scrollZone ? pointer.y - top - scrollZone : pointer.y > bottom - scrollZone ? pointer.y - bottom + scrollZone : 0
      if (into !== 0)
        scroller.scrollTop += Math.max(-1, Math.min(1, into / scrollZone)) * maxScrollStep
    }

    const paint = () => {
      autoScroll()
      const all = rows()
      const index = insertionIndex(all, pointer.y)
      const line = lineRef.current
      if (line && all.length > 0) {
        const plan = moved && dropAt(latestRef.current.keys, moved, index)
        const edge = index < all.length ? all[index].getBoundingClientRect().top : all[all.length - 1].getBoundingClientRect().bottom
        line.style.transform = `translateY(${edge - list.getBoundingClientRect().top}px)`
        line.style.visibility = plan ? 'visible' : 'hidden'
      }
      const label = labelRef.current
      if (label) {
        label.style.transform = `translate(${pointer.x + 14}px, ${pointer.y + 12}px)`
        label.style.visibility = 'visible'
      }
      frame = requestAnimationFrame(paint)
    }

    function onMove(move: PointerEvent) {
      if (move.pointerId !== pointerId)
        return
      pointer.x = move.clientX
      pointer.y = move.clientY
      if (moved || Math.hypot(pointer.x - start.x, pointer.y - start.y) <= dragThreshold)
        return
      const { selected } = latestRef.current
      moved = selected.has(key) ? selected : new Set([key])
      setDragging(moved)
      window.addEventListener('keydown', onKey, true)
      frame = requestAnimationFrame(paint)
    }

    function endVisuals() {
      cancelAnimationFrame(frame)
      window.removeEventListener('keydown', onKey, true)
      setDragging(null)
    }

    function onUp(up: PointerEvent) {
      if (up.pointerId !== pointerId)
        return
      const dropped = moved
      stop()
      if (!dropped)
        return
      swallowNextClick()
      latestRef.current.onDrop(dropAt(latestRef.current.keys, dropped, insertionIndex(rows(), up.clientY)))
    }

    function onCancel(cancel: PointerEvent) {
      if (cancel.pointerId === pointerId)
        stop()
    }

    // 按 Esc 取消之后指针仍然按着，松开时的 click 同样要吞掉。
    function onKey(keyboard: KeyboardEvent) {
      if (keyboard.key !== 'Escape')
        return
      keyboard.preventDefault()
      keyboard.stopPropagation()
      endVisuals()
      document.removeEventListener('pointermove', onMove)
      document.removeEventListener('pointerup', onUp)
      document.addEventListener('pointerup', onCancelledUp)
    }

    function onCancelledUp(up: PointerEvent) {
      if (up.pointerId !== pointerId)
        return
      stop()
      swallowNextClick()
    }

    function stop() {
      endVisuals()
      document.removeEventListener('pointermove', onMove)
      document.removeEventListener('pointerup', onUp)
      document.removeEventListener('pointerup', onCancelledUp)
      document.removeEventListener('pointercancel', onCancel)
      stopRef.current = null
    }

    document.addEventListener('pointermove', onMove)
    document.addEventListener('pointerup', onUp)
    document.addEventListener('pointercancel', onCancel)
    stopRef.current = stop
  }

  return { dragging, onPointerDown }
}
