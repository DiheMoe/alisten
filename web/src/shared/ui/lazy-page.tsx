import type { ComponentType } from 'react'
import { use, useEffect, useId, useReducer } from 'react'
import { PageLoadFailed } from './page-load-failed'

// Vite 的预加载包装在同一文档中对每个样式文件只请求一次，失败后也不再请求：重试时脚本能重新取到，样式文件却不会再请求。
// <link> 的 error 事件不冒泡，只能在捕获阶段记录失败的样式文件。
const failedStyles = new WeakSet<HTMLLinkElement>()
let watchingStyles = false

function watchStyles(): void {
  if (watchingStyles)
    return
  watchingStyles = true
  document.addEventListener('error', (event) => {
    if (event.target instanceof HTMLLinkElement && event.target.rel === 'stylesheet')
      failedStyles.add(event.target)
  }, true)
}

function stylesheets(): HTMLLinkElement[] {
  return [...document.querySelectorAll<HTMLLinkElement>('link[rel=stylesheet]')]
}

// 旧的 <link> 已被其他代码移出文档时，把新的加到 head 末尾：不在文档中的 <link> 永远不会触发 load 或 error。
function requestAgain(link: HTMLLinkElement): Promise<HTMLLinkElement> {
  const next = link.cloneNode() as HTMLLinkElement
  const settled = new Promise<HTMLLinkElement>((resolve) => {
    next.addEventListener('load', () => resolve(next))
    next.addEventListener('error', () => resolve(next))
  })
  if (link.isConnected)
    link.replaceWith(next)
  else
    document.head.append(next)
  return settled
}

// load 只写成 import(...).then(module => ...)，失败由 lazyPage 处理。不要写成 .then(ok, fail)：Vite 会把 import
// 和紧随其后的 .then 一起放进预加载包装，样式文件预加载失败时包装直接拒绝，这个 .then 不会被调用，fail 也就不会执行。
export function lazyPage<P extends object>(load: () => Promise<ComponentType<P>>): ComponentType<P> {
  let Loaded: ComponentType<P> | null = null
  let loading: Promise<void> | undefined
  let failures = 0
  let failedLinks: HTMLLinkElement[] = []
  // 失败提示显示过一次即失效：离开后再回来的新页面实例会重新加载，不会再看到旧的失败。
  let shownTo: string | null = null

  async function fetchPage(): Promise<ComponentType<P>> {
    if (failedLinks.length > 0) {
      const requested = await Promise.all(failedLinks.map(requestAgain))
      failedLinks = requested.filter(link => failedStyles.has(link))
      // 缺少样式的页面无法正常使用，样式文件仍取不到时本次重试按失败处理。
      if (failedLinks.length > 0)
        throw new Error('样式文件仍然取不到')
    }
    const known = new Set(stylesheets())
    const importing = load()
    // 预加载包装在 load() 里同步地把这个页面的样式文件加到 head，并等待它们。
    const own = stylesheets().filter(link => !known.has(link))
    return importing.catch((error: unknown) => {
      failedLinks = own.filter(link => failedStyles.has(link))
      throw error
    })
  }

  const start = () => {
    watchStyles()
    shownTo = null
    loading = fetchPage().then(
      (component) => {
        Loaded = component
        failures = 0
      },
      () => {
        failures += 1
      },
    )
    return loading
  }

  return function Page(props: P) {
    const id = useId()
    const [, update] = useReducer((count: number) => count + 1, 0)
    if (!Loaded && shownTo !== null && shownTo !== id)
      start()
    use(loading ?? start())
    useEffect(() => {
      if (!Loaded)
        shownTo = id
    })
    if (Loaded)
      return <Loaded {...props} />
    return (
      <PageLoadFailed
        failedAgain={failures > 1}
        onRetry={() => {
          start()
          update()
        }}
      />
    )
  }
}
