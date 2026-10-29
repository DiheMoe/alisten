import type { NowPlaying } from '@/core/realtime'

export interface MediaSessionHandlers {
  play: () => void
  pause: () => void
  nexttrack: () => void
}

const actions = ['play', 'pause', 'nexttrack'] as const

export function showMediaSession(nowPlaying: NowPlaying | null): void {
  if (!('mediaSession' in navigator))
    return
  if (!nowPlaying) {
    navigator.mediaSession.metadata = null
    navigator.mediaSession.playbackState = 'none'
    return
  }
  const { title, artist, album, coverUrl } = nowPlaying.entry.meta
  navigator.mediaSession.metadata = new MediaMetadata({ title, artist, album, artwork: coverUrl ? [{ src: coverUrl }] : [] })
}

export function setMediaSessionHandlers(handlers: MediaSessionHandlers | null): void {
  if (!('mediaSession' in navigator))
    return
  for (const action of actions) {
    try {
      navigator.mediaSession.setActionHandler(action, handlers ? handlers[action] : null)
    }
    catch {
      // 浏览器不支持某个操作时会抛出异常，其余操作照常设置。
    }
  }
}
