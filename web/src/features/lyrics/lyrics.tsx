import { AlignLeft } from 'lucide-react'
import { useEffect, useRef } from 'react'
import { performanceStore } from '@/features/settings'
import { useStore } from '@/shared/lib/use-store'
import { EmptyState } from '@/shared/ui'
import { lyricsStore } from './lyrics-store'
import './lyrics.css'

// 空白行不渲染节点：当前行为空白行时，滚动到前后两行的中间。
function centerLine(container: HTMLElement, nodes: Map<number, HTMLElement>, index: number, smooth: boolean): void {
  let before: HTMLElement | undefined
  let after: HTMLElement | undefined
  for (const [lineIndex, node] of nodes) {
    if (lineIndex <= index)
      before = node
    if (lineIndex >= index) {
      after = node
      break
    }
  }
  before ??= after
  after ??= before
  if (!before || !after)
    return
  const center = (before.offsetTop + before.clientHeight / 2 + after.offsetTop + after.clientHeight / 2) / 2
  container.scrollTo({ top: Math.max(0, center - container.clientHeight / 2), behavior: smooth ? 'smooth' : 'instant' })
}

export function Lyrics() {
  const lines = useStore(lyricsStore, s => s.lines)
  const scrollRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const container = scrollRef.current!
    const nodes = new Map(Array.from(container.querySelectorAll<HTMLElement>('[data-lyric-index]'), node => [Number(node.dataset.lyricIndex), node]))
    // React 会复用 key 相同的节点，需要先清除上一首歌留下的高亮。
    for (const node of nodes.values()) {
      node.classList.remove('active-lyric', 'near-lyric')
      node.removeAttribute('aria-current')
    }
    let active = -2
    let near: Element[] = []
    let frame: number | null = null
    const highlight = () => {
      const index = lyricsStore.state.index
      if (index === active)
        return
      const previous = nodes.get(active)
      for (const node of near)
        node.classList.remove('near-lyric')
      near = []
      previous?.classList.remove('active-lyric')
      previous?.removeAttribute('aria-current')
      const current = nodes.get(index)
      if (current) {
        current.classList.add('active-lyric')
        current.setAttribute('aria-current', 'true')
        near = [current.previousElementSibling, current.nextElementSibling].filter((node): node is Element => node !== null)
      }
      else {
        let before: HTMLElement | undefined
        for (const [lineIndex, node] of nodes) {
          if (lineIndex < index) {
            before = node
          }
          else {
            near.push(node)
            break
          }
        }
        if (before)
          near.push(before)
      }
      for (const node of near)
        node.classList.add('near-lyric')
      active = index
    }
    const onChange = () => {
      highlight()
      frame ??= requestAnimationFrame(() => {
        frame = null
        centerLine(container, nodes, lyricsStore.state.index, performanceStore.smoothMotion())
      })
    }
    highlight()
    centerLine(container, nodes, lyricsStore.state.index, false)
    const resizeObserver = new ResizeObserver(() => centerLine(container, nodes, lyricsStore.state.index, false))
    resizeObserver.observe(container)
    lyricsStore.addEventListener('change', onChange)
    return () => {
      resizeObserver.disconnect()
      lyricsStore.removeEventListener('change', onChange)
      if (frame !== null)
        cancelAnimationFrame(frame)
    }
  }, [lines])
  return (
    <section className="lyrics-panel" aria-label="同步歌词">
      <div className="lyrics-scroll" ref={scrollRef}>
        {lines.some(line => line.text)
          ? lines.map((line, index) => line.text && (
              <p className="lyric-line text-ui-xl" data-lyric-index={index} key={`${line.timeMs}:${line.text}`}>{line.text}</p>
            ))
          : <EmptyState icon={AlignLeft} title="歌词随音乐浮现">播放带有歌词的歌曲时，会在这里同步显示。</EmptyState>}
      </div>
    </section>
  )
}

export function ListenPage() {
  return <div className="listen-page"><Lyrics /></div>
}
