import type { LucideIcon } from 'lucide-react'
import type { KeyboardEvent, ReactNode } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useEffect, useRef, useSyncExternalStore } from 'react'

export interface SectionItem<T extends string> {
  id: T
  label: string
  icon: LucideIcon
  badge?: boolean
  // 手机端分类列表里显示在这一行右侧的当前值。
  summary?: string
  // 手机端分类列表里不单独列出这一项，比如 header 已经是它的入口。
  hiddenInList?: boolean
}

let wideQuery: MediaQueryList | undefined

function wide(): MediaQueryList {
  wideQuery ??= window.matchMedia('(min-width: 768px)')
  return wideQuery
}

function subscribeWide(notify: () => void): () => void {
  wide().addEventListener('change', notify)
  return () => wide().removeEventListener('change', notify)
}

function focusHeading(container: HTMLElement | null): void {
  const heading = container?.querySelector('h2')
  if (!heading)
    return
  if (!heading.hasAttribute('tabindex'))
    heading.tabIndex = -1
  heading.focus({ preventScroll: true })
}

const keySteps: Partial<Record<string, (index: number, count: number) => number>> = {
  ArrowDown: (index, count) => (index + 1) % count,
  ArrowUp: (index, count) => (index - 1 + count) % count,
  Home: () => 0,
  End: (_, count) => count - 1,
}

function moveFocus(event: KeyboardEvent<HTMLElement>): void {
  const step = keySteps[event.key]
  const items = [...event.currentTarget.querySelectorAll<HTMLElement>('[data-section]')]
  const index = items.indexOf(document.activeElement as HTMLElement)
  if (!step || index === -1)
    return
  event.preventDefault()
  items[step(index, items.length)].focus()
}

// 宽屏：左侧分类栏加右侧内容。current 为 null 时按 defaultSection 高亮，children 也应当是 defaultSection 的内容。
// 窄屏：current 为 null 时只显示分类列表，header 在列表上方。current 不为 null 时只显示 children，顶部的返回按钮调用 onSelect(null)。
// header 里带 data-section 的元素是对应分类的入口，比如 hiddenInList 的那一项。从详情回到列表时，焦点落回这个入口。
// children 里第一个 h2 是分类的标题。从分类栏切换过去之后，焦点落在这个标题上。
export function SectionLayout<T extends string>({ title, eyebrow, sections, current, defaultSection, onSelect, header, footer, children }: {
  title: string
  eyebrow?: string
  sections: SectionItem<T>[]
  current: T | null
  defaultSection: T
  onSelect: (id: T | null) => void
  header?: ReactNode
  footer?: ReactNode
  children: ReactNode
}) {
  const isWide = useSyncExternalStore(subscribeWide, () => wide().matches)
  const navRef = useRef<HTMLElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  // 地址改写之后 current 才会变，所以先记下切换完成后焦点要不要去标题。
  const focusContentRef = useRef(false)
  const previousRef = useRef(current)
  const shown = current ?? defaultSection

  useEffect(() => {
    const previous = previousRef.current
    previousRef.current = current
    const toContent = focusContentRef.current
    focusContentRef.current = false
    if (previous === current)
      return
    // 窄屏上列表和详情互相替换，宽屏上内容区按分类重新挂载，所以原来有焦点的元素已经不在了，焦点落到了 body 上。
    // 切换也可能来自 header 里的入口，或者浏览器的后退、前进。焦点还在分类栏或顶栏上时就留在原处。
    const narrow = !wide().matches
    if (narrow && current === null)
      [...listRef.current?.querySelectorAll<HTMLElement>('[data-section]') ?? []].find(item => item.dataset.section === previous)?.focus()
    else if (toContent || (narrow && previous === null) || document.activeElement === document.body)
      focusHeading(contentRef.current)
  }, [current])

  // 窄的主区域里分类栏是横向滚动的一行，当前项可能在视野之外。
  useEffect(() => {
    navRef.current?.querySelector('[aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [shown])

  // 宽屏上 current 为 null 时显示的就是 defaultSection，所以这时点 defaultSection 不改地址，免得多出一条看不出区别的记录。
  const select = (id: T) => {
    if (id === (isWide ? shown : current)) {
      focusHeading(contentRef.current)
      return
    }
    focusContentRef.current = true
    onSelect(id)
  }

  const heading = (
    <div className="section-layout-heading">
      {eyebrow && <p className="section-layout-eyebrow">{eyebrow}</p>}
      <h1>{title}</h1>
    </div>
  )
  const badge = <span className="section-badge"><span className="sr-only">需要处理</span></span>

  if (isWide) {
    return (
      <div className="section-layout">
        <div className="section-layout-frame" data-layout="split">
          <div className="section-layout-sidebar">
            {heading}
            <nav ref={navRef} className="section-nav" aria-label={title} onKeyDown={moveFocus}>
              {sections.map(({ id, label, icon: Icon, badge: flagged }) => (
                <button
                  type="button"
                  key={id}
                  data-section={id}
                  className="section-nav-item"
                  aria-current={id === shown ? 'page' : undefined}
                  onClick={() => select(id)}
                >
                  <Icon size={18} strokeWidth={1.8} aria-hidden="true" />
                  <span className="section-nav-label">{label}</span>
                  {flagged && badge}
                </button>
              ))}
            </nav>
            {footer && <div className="section-layout-footer">{footer}</div>}
          </div>
          <div ref={contentRef} className="section-layout-content" key={shown}>
            <div className="section-layout-main">{children}</div>
          </div>
        </div>
      </div>
    )
  }

  if (current === null) {
    return (
      <div className="section-layout">
        <div ref={listRef} className="section-layout-list" key="list">
          {heading}
          {header}
          <nav ref={navRef} className="section-group" aria-label={title} onKeyDown={moveFocus}>
            {sections.filter(section => !section.hiddenInList).map(({ id, label, icon: Icon, badge: flagged, summary }) => (
              <button type="button" key={id} data-section={id} className="section-row section-list-item" onClick={() => select(id)}>
                <Icon size={20} strokeWidth={1.8} aria-hidden="true" />
                <span className="section-list-label">{label}</span>
                {flagged && badge}
                {summary && <span className="section-list-summary">{summary}</span>}
                <ChevronRight size={18} className="section-list-chevron" aria-hidden="true" />
              </button>
            ))}
          </nav>
          {footer && <div className="section-layout-footer">{footer}</div>}
        </div>
      </div>
    )
  }

  return (
    <div className="section-layout">
      <div ref={contentRef} className="section-layout-content" key={current}>
        <button
          type="button"
          className="section-layout-back"
          aria-label={`返回${title}`}
          onClick={() => onSelect(null)}
        >
          <ChevronLeft size={20} aria-hidden="true" />
          {title}
        </button>
        <div className="section-layout-main">{children}</div>
      </div>
    </div>
  )
}
