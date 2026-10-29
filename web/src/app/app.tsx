import type { LucideIcon } from 'lucide-react'
import type { Page, Panel, RoomPage } from './route'
import type { SearchQuery } from '@/features/search'
import { ChevronLeft, ChevronRight, Headphones, History, House, ListMusic, MessageCircle, MoreHorizontal, Search, Settings, Share2, UserRound, Wifi, WifiOff, X } from 'lucide-react'
import { Suspense } from 'react'
import { roomStore } from '@/core/room'
import { ChatPanel } from '@/features/chat'
import { HistoryPage } from '@/features/history'
import { ListenPage } from '@/features/lyrics'
import { AudioEngine, ManualStartPrompt, PlayerBar, playerStore } from '@/features/playback'
import { QueuePage, voteSkip } from '@/features/queue'
import { copyInviteLink, EntryDialog, Lobby, roomEntryStore, RoomGate } from '@/features/rooms'
import { HeaderSearch, PlaylistPage, SearchPage, searchStore } from '@/features/search'
import { profileStore, SettingsPage } from '@/features/settings'
import { useStore } from '@/shared/lib/use-store'
import { Avatar, IconButton, lazyPage, Notifications, notificationStore, Skeleton } from '@/shared/ui'
import { DetailPanel } from './detail-panel'
import { ErrorBoundary, RegionFailed } from './error-boundary'
import { navigationStore } from './navigation'
import { pwaStore } from './pwa'
import { UpdatePrompt } from './update-prompt'
import { withViewTransition } from './view-transition'

// adminStore 不在 main.tsx 中启动：静态导入会把后台页打包进入口 chunk。
const AdminPage = lazyPage(() => import('@/features/admin').then((module) => {
  module.adminStore.start()
  return module.AdminPage
}))

const pageLoading = <div className="page-content"><Skeleton rows={6} /></div>

const menu: { page: 'lobby' | 'settings' | 'history' | RoomPage, label: string, icon: LucideIcon }[] = [
  { page: 'listen', label: '正在聆听', icon: Headphones },
  { page: 'queue', label: '播放列表', icon: ListMusic },
  { page: 'chat', label: '房间聊天', icon: MessageCircle },
  { page: 'history', label: '播放历史', icon: History },
  { page: 'lobby', label: '发现房间', icon: House },
  { page: 'settings', label: '设置', icon: Settings },
]

const mobileMenu: { page: RoomPage, label: string, icon: LucideIcon }[] = [
  { page: 'listen', label: '聆听', icon: Headphones },
  { page: 'queue', label: '播放列表', icon: ListMusic },
  { page: 'chat', label: '聊天', icon: MessageCircle },
]

const pageClasses: Partial<Record<Page, string>> = {
  queue: 'scrollbar-hidden',
  history: 'scrollbar-hidden',
  settings: 'settings-view',
}

function closeMenu(element: HTMLElement): void {
  element.closest('details')?.removeAttribute('open')
}

function search(query: SearchQuery): void {
  if (roomEntryStore.state.phase === 'idle')
    notificationStore.info('先加入一个房间，再搜索和点歌。')
  navigationStore.search(query)
}

function ConnectionStatus() {
  const status = useStore(roomStore, s => s.status)
  const inRoom = status !== null && status !== 'stopped'
  const connected = status === 'open'
  return (
    <button
      type="button"
      className={`connection-status text-ui-xs ${connected ? 'connected' : ''}`}
      title={inRoom && !connected ? '点击重试连接' : '房间连接状态'}
      disabled={!inRoom || connected}
      onClick={() => roomStore.reconnect()}
    >
      {connected ? <Wifi size={12} /> : <WifiOff size={12} />}
      <span>{!inRoom ? '未加入' : connected ? '同步在线' : status === 'connecting' ? '连接中' : '重连中'}</span>
    </button>
  )
}

function ProfileButton() {
  const member = useStore(profileStore, s => s.member)
  return (
    <button type="button" className="profile-button" aria-label="设置" title={member?.name ?? '设置'} onClick={() => navigationStore.open('settings')}>
      {member ? <Avatar hash={member.avatarHash} name={member.name} className="text-ui-base" /> : <UserRound size={20} strokeWidth={1.7} aria-hidden="true" />}
    </button>
  )
}

function Header({ page }: { page: Page }) {
  const canBack = useStore(navigationStore, s => s.canBack)
  const canForward = useStore(navigationStore, s => s.canForward)
  const inRoom = useStore(roomEntryStore, s => s.phase !== 'idle')
  const joined = useStore(roomEntryStore, s => s.phase === 'joined')
  const roomName = useStore(roomEntryStore, s => s.room?.name)
  const home = inRoom ? 'listen' : 'lobby'
  return (
    <header className="workspace-header">
      <div className="header-leading">
        <details
          className="header-menu"
          onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget as Node | null))
              event.currentTarget.removeAttribute('open')
          }}
          onKeyDown={(event) => {
            if (event.key === 'Escape')
              event.currentTarget.removeAttribute('open')
          }}
        >
          <summary aria-label="更多功能" title="更多功能">
            <MoreHorizontal size={20} />
          </summary>
          <nav className="main-nav" aria-label="主导航">
            {menu.map(({ page: target, label, icon: Icon }) => (
              <button
                type="button"
                key={target}
                className={`nav-item text-ui-base ${page === target ? 'selected' : ''}`}
                aria-current={page === target ? 'page' : undefined}
                onClick={(event) => {
                  navigationStore.open(target)
                  closeMenu(event.currentTarget)
                }}
              >
                <Icon size={20} strokeWidth={1.7} />
                {label}
              </button>
            ))}
            <button
              type="button"
              className="nav-item text-ui-base"
              disabled={!joined}
              onClick={(event) => {
                const { room } = roomEntryStore.state
                if (room)
                  void copyInviteLink(room)
                closeMenu(event.currentTarget)
              }}
            >
              <Share2 size={20} />
              复制邀请链接
            </button>
          </nav>
        </details>
        <IconButton icon={ChevronLeft} label="后退" disabled={!canBack} onClick={() => navigationStore.back()} />
        <IconButton icon={ChevronRight} label="前进" className="header-forward" disabled={!canForward} onClick={() => navigationStore.forward()} />
      </div>
      <div className="header-search-area">
        <IconButton
          icon={House}
          label={inRoom ? '返回聆听' : '发现房间'}
          title={inRoom ? roomName : '发现房间'}
          className="home-widget"
          aria-current={page === home ? 'page' : undefined}
          onClick={() => navigationStore.open(home)}
        />
        <HeaderSearch onSearch={search} />
        <IconButton
          icon={Search}
          label="搜索音乐"
          className="mobile-search-button"
          aria-current={page === 'search' ? 'page' : undefined}
          onClick={() => search(searchStore.query)}
        />
      </div>
      <div className="header-actions">
        <ConnectionStatus />
        <ProfileButton />
      </div>
    </header>
  )
}

// 页面自己订阅地址里的参数：筛选框每按一次键都改写地址，应用壳不必跟着重新渲染。
function RoutedLobby({ onEntered }: { onEntered: (ref: string) => void }) {
  const route = useStore(navigationStore, s => s.route)
  if (route.name !== 'lobby')
    return null
  return <Lobby filter={route.filter} onFilter={filter => navigationStore.navigate({ ...route, filter })} onEntered={onEntered} />
}

function RoutedHistory() {
  const route = useStore(navigationStore, s => s.route)
  if (route.name !== 'history')
    return null
  return <HistoryPage filter={route.filter} onFilter={filter => navigationStore.navigate({ ...route, filter })} />
}

function RoutedSearch() {
  const route = useStore(navigationStore, s => s.route)
  if (route.name !== 'search')
    return null
  return (
    <SearchPage
      inlineSearchClassName="mobile-search-box"
      onSearch={search}
      onOpenPlaylist={({ source, id, name }) => navigationStore.navigate({ name: 'playlist', ref: route.ref, panel: route.panel, source, id, page: 1 }, name)}
    />
  )
}

function RoutedPlaylist() {
  const route = useStore(navigationStore, s => s.route)
  const title = useStore(navigationStore, s => s.title)
  if (route.name !== 'playlist')
    return null
  return <PlaylistPage title={title} page={route.page} onPage={page => navigationStore.navigate({ ...route, page })} />
}

export function App() {
  const page = useStore(navigationStore, s => s.route.name)
  const panel = useStore(navigationStore, s => s.route.panel)
  const joined = useStore(roomEntryStore, s => s.phase === 'joined')
  const songStartedAtMs = useStore(roomStore, s => s.room?.nowPlaying?.startedAtMs)
  const browse = () => navigationStore.open('lobby')
  const showRoom = (ref: string) => navigationStore.navigate({ name: 'listen', ref, panel: null })
  // 加入房间前不显示面板：底部的切换按钮此时不可用，而聊天视图的关闭按钮在 RoomGate 内，打开的聊天面板会无法关闭。
  const shownPanel = joined && panel !== page ? panel : null
  const switchPanel = (next: Panel | null) => {
    if (shownPanel && next && next !== shownPanel && next !== page)
      withViewTransition(() => navigationStore.setPanel(next))
    else
      navigationStore.setPanel(next)
  }
  const togglePanel = (target: 'queue' | 'chat') => {
    const { route } = navigationStore.state
    if (window.matchMedia('(max-width: 767px)').matches) {
      navigationStore.open(target)
    }
    else if (route.name === target) {
      navigationStore.navigate({ name: 'listen', ref: route.ref, panel: route.panel === target ? null : route.panel })
    }
    else {
      switchPanel(panel === target ? null : target)
    }
  }
  return (
    <>
      {/* 播放引擎在房间为空时就已挂载，出错只可能源于运行环境，手动重试很可能同样失败，因此不提供重试按钮。 */}
      <ErrorBoundary fallback={null} resetKey={songStartedAtMs} onError={() => notificationStore.error('音乐无法播放。下一首歌开始时会自动再试，也可以刷新页面再试。', 8000)}>
        <AudioEngine onNextTrack={() => void voteSkip()} />
      </ErrorBoundary>
      <div className="app-shell">
        <div className="workspace">
          <ErrorBoundary fallback={(retry, retryButton) => <header className="workspace-header region-failed"><RegionFailed message="顶栏出错了" retry={retry} retryButton={retryButton} /></header>}>
            <Header page={page} />
          </ErrorBoundary>
          <div className="workspace-body">
            <main className={`main-content ${pageClasses[page] ?? ''}`} key={page}>
              <ErrorBoundary>
                <Suspense fallback={pageLoading}>
                  {page === 'lobby' && <RoutedLobby onEntered={showRoom} />}
                  {page === 'admin' && <AdminPage onEntered={showRoom} />}
                  {page === 'listen' && <RoomGate onBrowse={browse}><ListenPage /></RoomGate>}
                  {page === 'queue' && <RoomGate onBrowse={browse}><QueuePage /></RoomGate>}
                  {page === 'search' && <RoomGate onBrowse={browse}><RoutedSearch /></RoomGate>}
                  {page === 'playlist' && <RoomGate onBrowse={browse}><RoutedPlaylist /></RoomGate>}
                  {page === 'chat' && <RoomGate onBrowse={browse}><ChatPanel /></RoomGate>}
                  {page === 'history' && <RoutedHistory />}
                  {page === 'settings' && <SettingsPage />}
                </Suspense>
              </ErrorBoundary>
            </main>
            <DetailPanel view={shownPanel}>
              {view => view === 'chat'
                ? <ErrorBoundary><Suspense fallback={pageLoading}><RoomGate onBrowse={browse}><ChatPanel compact onClose={() => switchPanel(null)} /></RoomGate></Suspense></ErrorBoundary>
                : (
                    <>
                      <div className="detail-tabs" aria-label="侧栏视图">
                        <button type="button" className="detail-tab text-ui-base" aria-pressed={view === 'queue'} disabled={page === 'queue'} onClick={() => switchPanel('queue')}>播放列表</button>
                        <button type="button" className="detail-tab text-ui-base" aria-pressed={view === 'history'} disabled={page === 'history'} onClick={() => switchPanel('history')}>播放历史</button>
                        <IconButton icon={X} label="关闭侧栏" onClick={() => switchPanel(null)} />
                      </div>
                      <ErrorBoundary>
                        <Suspense fallback={pageLoading}>
                          {view === 'queue' ? <RoomGate onBrowse={browse}><QueuePage compact /></RoomGate> : <HistoryPage compact />}
                        </Suspense>
                      </ErrorBoundary>
                    </>
                  )}
            </DetailPanel>
          </div>
        </div>
        <ErrorBoundary fallback={(retry, retryButton) => <section className="player-bar region-failed" aria-label="播放器"><RegionFailed message="播放器出错了" retry={retry} retryButton={retryButton} /></section>}>
          <PlayerBar
            onSkip={voteSkip}
            onLyrics={() => navigationStore.open('listen')}
            onQueue={() => togglePanel('queue')}
            onChat={() => togglePanel('chat')}
            queueOpen={joined && (page === 'queue' || panel === 'queue')}
            chatOpen={joined && (page === 'chat' || panel === 'chat')}
            canOpenPanels={joined}
          />
        </ErrorBoundary>
        <nav className="mobile-nav" aria-label="底部导航">
          {mobileMenu.map(({ page: target, label, icon: Icon }) => (
            <button
              type="button"
              className={`text-ui-xs ${page === target ? 'selected' : ''}`}
              key={target}
              aria-current={page === target ? 'page' : undefined}
              onClick={() => navigationStore.open(target)}
            >
              <Icon size={24} strokeWidth={1.7} />
              <span>{label}</span>
            </button>
          ))}
        </nav>
      </div>
      <ErrorBoundary
        fallback={null}
        resetOn={roomEntryStore}
        onError={() => {
          // 弹窗已不再显示，进房流程却仍停在等待票据，页面会一直显示骨架屏：与关闭弹窗一样，放弃这次进入。
          navigationStore.leaveRoom()
          roomEntryStore.reset()
          notificationStore.error('加入房间的窗口出错了，没有加入房间，再选一次房间。')
        }}
      >
        <EntryDialog onCancel={() => navigationStore.leaveRoom()} />
      </ErrorBoundary>
      <ErrorBoundary fallback={null} announce="开始播放的提示出错了。点一下页面，下一首歌会自动播放。" resetOn={playerStore}>
        <ManualStartPrompt />
      </ErrorBoundary>
      <ErrorBoundary fallback={null} announce="更新提示出错了。关闭应用再打开，就会用上新版本。" resetOn={pwaStore}>
        <UpdatePrompt />
      </ErrorBoundary>
      <ErrorBoundary fallback={null} announce="通知出错了，稍后恢复。" resetOn={notificationStore}>
        <Notifications />
      </ErrorBoundary>
    </>
  )
}
