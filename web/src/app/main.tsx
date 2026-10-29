import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { brandStore } from '@/core/brand'
import { chatStore } from '@/features/chat'
import { historyStore } from '@/features/history'
import { lyricsStore } from '@/features/lyrics'
import { playerStore, reminderStore, volumeStore } from '@/features/playback'
import { roomEntryStore } from '@/features/rooms'
import { searchStore } from '@/features/search'
import { appearanceStore, performanceStore, profileStore } from '@/features/settings'
import { setViewportHeight } from '@/shared/lib/viewport'
import { App } from './app'
import { watchConnection } from './connection-notices'
import { navigationStore } from './navigation'
import { pwaStore } from './pwa'
import './style.css'

const root = document.getElementById('root')
if (!root)
  throw new Error('index.html 缺少 #root 节点')

// 在首次渲染和进入房间之前启动：外观、性能等级、音量和品牌名要在第一帧生效，订阅房间的 Store 不能错过进入房间。
// 聊天面板按需加载，歌词页也不常驻，但聊天、搜索和歌词的 Store 要从进入房间起跟随房间变化，不能等到页面挂载。
appearanceStore.start()
performanceStore.start()
volumeStore.start()
brandStore.start()
profileStore.start()
playerStore.start()
reminderStore.start()
historyStore.start()
roomEntryStore.start()
chatStore.start()
searchStore.start()
lyricsStore.start()
// 启动时获取成员身份，并借此发现已失效的成员 token。失败不影响浏览大厅，之后的请求会再次尝试。
void profileStore.load().catch(() => {})
watchConnection()
pwaStore.start()
navigationStore.start()
setViewportHeight()
window.addEventListener('resize', setViewportHeight, { passive: true })

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
