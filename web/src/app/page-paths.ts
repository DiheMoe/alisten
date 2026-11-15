import type { AdminSection } from '@/features/admin'
import type { SettingsSection } from '@/features/settings'

export const settingsSections: Record<SettingsSection, true> = { account: true, appearance: true, playback: true, performance: true, privacy: true }
export const adminSections: Record<AdminSection, true> = { site: true, registration: true, users: true, rooms: true, sources: true, media: true }

function oneOf(names: Record<string, true>): string {
  return Object.keys(names).join('|')
}

// 页面的路径，写成正则。parseRoute 认得的路径都要能匹配上，nginx 的页面 location 也要和这里一致，
// 否则在那个页面上刷新会得到 404。路径段写成 [^/?]+，这样同一份规则也能匹配带查询参数的地址。
const pagePaths = [
  `/(?:settings(?:/(?:${oneOf(settingsSections)}))?|admin(?:/(?:${oneOf(adminSections)}))?|history|playlists(?:/[^/?]+)?)?`,
  '/r/[^/?]+(?:/(?:queue|chat|search|playlists/[^/?]+/[^/?]+))?',
  '/s/[^/?]+',
  '/(?:pair|auth/github)',
]

// Service Worker 和 nginx 一样，只有页面路径返回 index.html，/api、/ws 和 /media 照常走网络。
// Workbox 拿路径加查询参数来匹配，nginx 只看路径，所以这里的规则末尾允许查询参数。
export const navigationAllowlist = pagePaths.map(path => new RegExp(`^${path}(?:\\?.*)?$`))
