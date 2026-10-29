import type { Panel, RoomPage, Route } from './route'
import type { SearchQuery } from '@/features/search'
import { roomEntryStore } from '@/features/rooms'
import { searchStore } from '@/features/search'
import { canGoBack, canGoForward, currentEntry, goBack, startHistory, writeEntry } from '@/shared/lib/session-history'
import { Store } from '@/shared/lib/store'
import { formatRoute, isNewEntry, leaveStep, parseRoute } from './route'

interface NavigationState {
  route: Route
  // 写入这条记录时已经知道的页面标题，比如打开的歌单的名字。
  title: string | undefined
  canBack: boolean
  canForward: boolean
}

const lobby: Route = { name: 'lobby', filter: '', panel: null }

function isCurrentRoom(ref: string): boolean {
  const { ref: entered, room } = roomEntryStore.state
  return ref === entered || ref === room?.id || ref === room?.slug
}

// 显示什么由地址决定；后退和前进就是浏览器历史记录的后退和前进。
class NavigationStore extends Store<NavigationState> {
  #narrow: MediaQueryList | null = null
  // 取消进入房间之后正在离开的房间：进房时的 ref、UUID 和别名。
  #leaving: ReadonlySet<string> | null = null

  constructor() {
    super({ route: lobby, title: undefined, canBack: false, canForward: false })
  }

  start(): void {
    const fresh = startHistory(this.#onPop)
    this.#narrow = window.matchMedia('(max-width: 767px)')
    this.#narrow.addEventListener('change', this.#onResize)
    const parsed = parseRoute(window.location)
    let route = this.#fit(parsed ?? lobby)
    // 上次的房间记在所有标签页共用的 localStorage 里，后台又常在另一个标签页打开：后台不恢复房间，免得两个标签页同时放歌。
    if (!('ref' in route) && route.name !== 'admin') {
      const lastRoom = roomEntryStore.resume()
      // 直接打开网站时进入上次的房间；刷新或者回到发现房间时停在原处。
      if (lastRoom && fresh && parsed && formatRoute(parsed) === '/')
        route = { name: 'listen', ref: lastRoom, panel: null }
    }
    const url = formatRoute(route)
    if (url !== window.location.pathname + window.location.search)
      writeEntry(url, true)
    this.#show(route)
  }

  navigate(route: Route, title?: string): void {
    const from = this.state.route
    const url = formatRoute(route)
    if (url === formatRoute(from))
      return
    writeEntry(url, !isNewEntry(from, route), title)
    this.#show(route)
  }

  replace(route: Route): void {
    writeEntry(formatRoute(route), true)
    this.#show(route)
  }

  // 不在房间里时，房间的页面没有地址，去发现房间。换页面时侧栏保持原样。
  open(page: 'lobby' | 'admin' | 'settings' | 'history' | RoomPage): void {
    const { panel } = this.state.route
    if (page === 'lobby' || page === 'history')
      this.navigate({ name: page, filter: '', panel })
    else if (page === 'admin' || page === 'settings')
      this.navigate({ name: page, panel })
    else
      this.#openRoomPage(ref => ({ name: page, ref, panel }))
  }

  search(query: SearchQuery): void {
    const { panel } = this.state.route
    this.#openRoomPage(ref => ({ name: 'search', ref, panel, query }))
  }

  setPanel(panel: Panel | null): void {
    this.navigate({ ...this.state.route, panel })
  }

  // 取消进入房间：要在 roomEntryStore.reset() 之前调用，离开时靠进房的信息认出这个房间的地址。
  leaveRoom(): void {
    const { ref, room } = roomEntryStore.state
    this.#leaving = new Set([ref, room?.id, room?.slug].filter(value => typeof value === 'string'))
    this.#leaveFrom(this.state.route, this.#leaving)
  }

  back(): void {
    window.history.back()
  }

  forward(): void {
    window.history.forward()
  }

  #openRoomPage(route: (ref: string) => Route): void {
    const { route: current } = this.state
    const ref = 'ref' in current ? current.ref : roomEntryStore.state.ref
    this.navigate(ref ? route(ref) : lobby)
  }

  // 返回这一步是否已经处理：后退了，或者换成了发现房间。
  #leaveFrom(route: Route, leaving: ReadonlySet<string>): boolean {
    switch (leaveStep(route, leaving, canGoBack(currentEntry().position))) {
      case 'back':
        goBack()
        return true
      case 'lobby':
        this.#leaving = null
        this.replace(lobby)
        return true
      case 'stay':
        this.#leaving = null
        return false
    }
  }

  // 窄屏没有侧栏：打开带侧栏的地址时去掉它。
  #fit(route: Route): Route {
    return this.#narrow?.matches && route.panel ? { ...route, panel: null } : route
  }

  #show(next: Route): void {
    // 关闭弹窗回到的记录地址不变：沿用原来的对象，订阅 route 的组件不必重新渲染。
    const route = formatRoute(next) === formatRoute(this.state.route) ? this.state.route : next
    if ('ref' in route && !isCurrentRoom(route.ref))
      void roomEntryStore.enter(route.ref)
    if (route.name === 'search')
      searchStore.show(route.query)
    else if (route.name === 'playlist')
      searchStore.showPlaylist(route.source, route.id)
    else
      searchStore.hide()
    const { position, title } = currentEntry()
    this.setState({ route, title, canBack: canGoBack(position), canForward: canGoForward(position) })
  }

  #onPop = (): void => {
    const parsed = parseRoute(window.location) ?? lobby
    if (this.#leaving && this.#leaveFrom(parsed, this.#leaving))
      return
    const route = this.#fit(parsed)
    if (route !== parsed)
      writeEntry(formatRoute(route), true)
    this.#show(route)
  }

  #onResize = (): void => {
    const route = this.#fit(this.state.route)
    if (route !== this.state.route)
      this.replace(route)
  }
}

export const navigationStore = new NavigationStore()
