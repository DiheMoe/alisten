import { flushSync } from 'react-dom'
import { performanceStore } from '@/features/settings'

export function canPushViews(): boolean {
  return 'startViewTransition' in document && performanceStore.smoothMotion()
}

export function withViewTransition(update: () => void): void {
  if (!canPushViews()) {
    update()
    return
  }
  document.startViewTransition(() => {
    // eslint-disable-next-line react/dom-no-flush-sync -- 视图过渡要求在更新回调中同步提交
    flushSync(update)
  })
}
