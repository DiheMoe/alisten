export interface Position {
  index: number
  // 已知的最后一条记录：后退之后，后面的记录还在，可以前进。
  last: number
}

export function afterPush(at: Position): Position {
  return { index: at.index + 1, last: at.index + 1 }
}

export function afterMarker(at: Position): Position {
  return { index: at.index, last: at.index }
}

export function afterPop(at: Position, index: number): Position {
  return { index, last: Math.max(at.last, index) }
}

export function canGoBack(at: Position): boolean {
  return at.index > 0
}

export function canGoForward(at: Position): boolean {
  return at.index < at.last
}

interface EntryState {
  index: number
  title?: string
  modal?: true
}

interface Write {
  url: string
  replace: boolean
  title: string | undefined
}

let position: Position = { index: 0, last: 0 }
let current: EntryState = { index: 0 }
let notify = (): void => {}
const modals: { close: () => void }[] = []
// 退回标记记录下面的页面要等到 popstate 才算完成，其间的写入排队，落在页面的记录上。
let awaitingBack = false
const queued: Write[] = []

function readEntry(state: unknown): EntryState | null {
  if (typeof state !== 'object' || state === null)
    return null
  const { index, title, modal } = state as Record<string, unknown>
  if (typeof index !== 'number' || !Number.isInteger(index) || index < 0)
    return null
  const entry: EntryState = { index }
  if (typeof title === 'string')
    entry.title = title
  if (modal === true)
    entry.modal = true
  return entry
}

// 调用太频繁时浏览器会抛出 SecurityError，输入筛选词时每次按键都要替换地址：这时只让地址暂时落后，页面照常显示。
// 写不进弹窗的标记记录时弹窗照常显示，返回键在关掉它的同时也会后退一页。
function write(change: () => void): boolean {
  try {
    change()
    return true
  }
  catch (error) {
    if (error instanceof DOMException && error.name === 'SecurityError')
      return false
    throw error
  }
}

function pushMarker(): void {
  const marker: EntryState = { ...current, modal: true }
  if (!write(() => window.history.pushState(marker, '', window.location.href)))
    return
  current = marker
  position = afterMarker(position)
}

function back(): void {
  awaitingBack = true
  window.history.back()
}

function onPop(event: PopStateEvent): void {
  current = readEntry(event.state) ?? { index: position.index }
  position = afterPop(position, current.index)
  if (awaitingBack) {
    awaitingBack = false
    for (const { url, replace, title } of queued.splice(0))
      writeEntry(url, replace, title)
    if (modals.length && !current.modal && !awaitingBack)
      pushMarker()
    notify()
    return
  }
  if (current.modal && !modals.length) {
    // 前进到了已经关闭的弹窗留下的标记记录：退回它下面的页面。
    back()
    return
  }
  // 先让页面跟上这条记录再关最上层的弹窗：关弹窗时可能接着导航，比如取消进入房间时后退。
  notify()
  modals.pop()?.close()
  if (modals.length && !current.modal && !awaitingBack)
    pushMarker()
}

// 返回当前记录是否第一次打开：刷新、前进和后退回到的记录都带着应用写入的状态。
export function startHistory(onChange: () => void): boolean {
  notify = onChange
  const entry = readEntry(window.history.state)
  current = { index: entry?.index ?? 0 }
  if (entry?.title !== undefined)
    current.title = entry.title
  // 刷新时弹窗已经关闭，它留下的标记记录改作页面的记录。
  if (!entry || entry.modal)
    window.history.replaceState(current, '')
  position = { index: current.index, last: current.index }
  window.addEventListener('popstate', onPop)
  return entry === null
}

export function currentEntry(): { position: Position, title: string | undefined } {
  return { position, title: current.title }
}

// 后退一个页面。弹窗的标记记录压在当前页面上（或者正在退掉它）时只退到页面的记录：落到那里时导航会收到通知，由它决定要不要接着后退。
export function goBack(): void {
  if (awaitingBack)
    return
  if (current.modal)
    back()
  else
    window.history.back()
}

export function writeEntry(url: string, replace: boolean, title?: string): void {
  if (awaitingBack) {
    queued.push({ url, replace, title })
    return
  }
  if (replace && current.modal) {
    // 弹窗的标记记录压在页面的记录上：先退回页面的记录，再替换它。
    queued.push({ url, replace, title })
    back()
    return
  }
  const entry: EntryState = { index: replace ? current.index : current.index + 1 }
  const entryTitle = replace ? title ?? current.title : title
  if (entryTitle !== undefined)
    entry.title = entryTitle
  // 在大厅的加入、创建房间弹窗里换页面时，弹窗随旧页面一起关闭，新页面的记录直接顶替标记记录。
  const written = write(() => replace || current.modal
    ? window.history.replaceState(entry, '', url)
    : window.history.pushState(entry, '', url))
  if (!written)
    return
  if (!replace)
    position = afterPush(position)
  current = entry
}

// 整个弹窗栈只占一条历史记录，返回键只关闭最上层的弹窗。
export function registerModalHistory(close: () => void): () => void {
  const modal = { close }
  modals.push(modal)
  if (!current.modal && !awaitingBack)
    pushMarker()
  return () => {
    const index = modals.indexOf(modal)
    if (index < 0)
      return
    modals.splice(index, 1)
    if (!modals.length && current.modal && !awaitingBack)
      back()
  }
}
