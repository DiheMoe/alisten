import type { Panel } from './route'
import { flushSync } from 'react-dom'
import { performanceStore } from '@/features/settings'

type ViewDirection = 'forward' | 'backward' | 'rise' | 'sink'

// 播放列表和播放历史是左右并排的 tab。聊天从播放栏升起，盖在这两个 tab 上面。
export function panelDirection(from: Panel, to: Panel): ViewDirection {
  if (to === 'chat')
    return 'rise'
  if (from === 'chat')
    return 'sink'
  return to === 'queue' ? 'backward' : 'forward'
}

export function canPushViews(): boolean {
  return 'startViewTransition' in document && performanceStore.smoothMotion()
}

export function withViewTransition(update: () => void, direction: ViewDirection): void {
  document.documentElement.dataset.viewDirection = direction
  if (!canPushViews()) {
    update()
    return
  }
  document.startViewTransition(() => {
    // eslint-disable-next-line react/dom-no-flush-sync -- 视图过渡要求在更新回调中同步提交
    flushSync(update)
  })
}
