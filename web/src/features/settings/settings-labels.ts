import type { Theme } from './appearance-store'
import type { PerformanceLevel } from './performance-store'

export const themeNames: Record<Theme, string> = { dark: '深色', light: '浅色', system: '跟随系统' }

export const levelNames: Record<PerformanceLevel, { name: string, description: string }> = {
  high: { name: '高质量', description: '所有动画效果，适合高性能设备。' },
  medium: { name: '平衡', description: '优化过的动画效果，推荐使用。' },
  low: { name: '省电', description: '简化动画效果，适合移动设备。' },
  off: { name: '极简', description: '关闭所有动画，对设备的负担最小。' },
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

// 申请没有拿到授权时的说明。被拒绝之后 notificationHint 已经写了去哪里允许；提示被关掉或者被浏览器收起时，权限仍是 default，
// 再申请可能不会弹出提示，所以也指向网站设置。
export function notificationRefusal(permission: NotificationPermission): string {
  return permission === 'denied' ? '浏览器拒绝了显示通知。' : '浏览器没有授权，可以在浏览器的网站设置里允许通知。'
}
