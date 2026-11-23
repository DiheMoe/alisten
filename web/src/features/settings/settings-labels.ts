import type { Theme } from './appearance-store'
import type { PerformanceLevel } from './performance-store'

export const themeNames: Record<Theme, string> = { dark: '深色', light: '浅色', system: '跟随系统' }

export const levelNames: Record<PerformanceLevel, { name: string, description: string }> = {
  standard: { name: '标准', description: '保留全部动画和过渡。' },
  low: { name: '省电', description: '页面和弹窗只淡入淡出，去掉光晕、悬停浮起和歌词的平滑滚动。' },
  off: { name: '极简', description: '关闭所有动画和过渡，去掉阴影和模糊。' },
}

export function sectionSummaries({ theme, level, volume, muted, recordListens }: {
  theme: Theme
  level: PerformanceLevel
  volume: number
  muted: boolean
  recordListens: boolean | null
}): { appearance: string, playback: string, performance: string, privacy?: string } {
  return {
    appearance: themeNames[theme],
    playback: muted ? '静音' : `${volume}%`,
    performance: levelNames[level].name,
    ...recordListens !== null && { privacy: recordListens ? '开' : '关' },
  }
}

// 浏览器拒绝之后，网页不能再弹出授权请求。
export function notificationHint(permission: NotificationPermission): { description: string, authorize: boolean } {
  switch (permission) {
    case 'granted':
      return { description: '轮到你点的歌时，在系统通知里提醒', authorize: false }
    case 'denied':
      return { description: '浏览器不允许显示通知，要在浏览器的网站设置里允许', authorize: false }
    default:
      return { description: '需要浏览器授权，现在还没授权', authorize: true }
  }
}

// 申请没有拿到授权时的说明。被拒绝之后，notificationHint 已经写了去哪里允许。提示被关掉或者被浏览器收起时，权限仍是 default，
// 再申请可能不会弹出提示，所以也指向网站设置。
export function notificationRefusal(permission: NotificationPermission): string {
  return permission === 'denied' ? '浏览器拒绝了显示通知。' : '浏览器没有授权，可以在浏览器的网站设置里允许通知。'
}
