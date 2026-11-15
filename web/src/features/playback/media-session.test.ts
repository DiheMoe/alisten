import type { NowPlaying } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { showMediaSession } from './media-session'

// 和浏览器一样，封面地址解析不了时构造函数抛出 TypeError。
class FakeMetadata {
  readonly title: string | undefined
  readonly artwork: string[]

  constructor(init: MediaMetadataInit) {
    this.title = init.title
    this.artwork = (init.artwork ?? []).map(({ src }) => new URL(src, 'https://alisten.test/').href)
  }
}

function playing(coverUrl: string): NowPlaying {
  return { entry: { meta: { title: '晴天', artist: '周杰伦', album: '叶惠美', coverUrl } } } as unknown as NowPlaying
}

let session: { metadata: FakeMetadata | null, playbackState: string }

beforeEach(() => {
  session = { metadata: null, playbackState: 'playing' }
  vi.stubGlobal('navigator', { mediaSession: session })
  vi.stubGlobal('MediaMetadata', FakeMetadata)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('showMediaSession', () => {
  it('带上这首歌的封面', () => {
    showMediaSession(playing('https://p1.music.126.net/a.jpg'))
    expect(session.metadata).toMatchObject({ title: '晴天', artwork: ['https://p1.music.126.net/a.jpg'] })
  })

  it('封面地址解析不了时不抛出，去掉封面照样显示', () => {
    showMediaSession(playing('https://'))
    expect(session.metadata).toMatchObject({ title: '晴天', artwork: [] })
  })

  it('没有在放的歌时清空', () => {
    showMediaSession(playing(''))
    showMediaSession(null)
    expect(session).toEqual({ metadata: null, playbackState: 'none' })
  })
})
