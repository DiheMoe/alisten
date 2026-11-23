import type { Entry, HelloResponse, Member } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { sockets, stubBrowser } from '@/core/realtime/fake-socket'
import { pickButton } from './pick'

const me: Member = { id: 'm1', name: 'alice', avatarHash: '' }

const hello: HelloResponse = {
  member: me,
  room: {
    term: 1,
    seq: 0,
    room: { id: 'r1', name: '房间', description: '', kind: 'persistent', capacity: 64 },
    playMode: 'sequential',
    nowPlaying: null,
    pending: null,
    next: null,
    waiting: [],
    members: { sessions: [], onlineCount: 0 },
  },
  serverTimeMs: 0,
}

// 服务端把 B 站的链接转成来源为 bilibili 的曲目，歌名只在服务端的响应里。
const video: Entry = {
  id: 'e1',
  track: { source: 'bilibili', id: 'BV1xx411c7mD' },
  meta: { title: '晴天', artist: '', album: '', coverUrl: '', pageUrl: '', durationMs: 0 },
  by: me,
  auto: false,
  likes: 0,
  status: 'preparing',
}

async function joined() {
  const pick = await import('./pick')
  const { roomStore } = await import('./room-store')
  const { notificationStore } = await import('@/shared/lib/notification-store')
  roomStore.join('r1', 't1')
  const socket = sockets.at(-1)
  if (!socket)
    throw new Error('还没有建立连接')
  socket.open()
  socket.respond(socket.last('hello'), hello)
  await vi.advanceTimersByTimeAsync(0)
  return { ...pick, socket, notifications: () => notificationStore.state.notifications }
}

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
  stubBrowser()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('pickButton', () => {
  const link = { source: 'link', id: 'https://example.com/song.mp3' } as const
  const video = { source: 'bilibili', id: 'BV1xx411c7mD' } as const

  it('连上房间时可以点歌', () => {
    expect(pickButton(video, '晴天', { connected: true, ephemeral: true })).toEqual({ label: '点歌《晴天》', disabled: false })
    expect(pickButton(link, '晴天', { connected: true, ephemeral: false })).toEqual({ label: '点歌《晴天》', disabled: false })
  })

  it('没有连上房间时禁用', () => {
    expect(pickButton(video, '晴天', { connected: false, ephemeral: false })).toEqual({ label: '点歌《晴天》', disabled: true })
  })

  it('临时房间里链接的歌不能点，并说明原因', () => {
    expect(pickButton(link, '晴天', { connected: true, ephemeral: true })).toEqual({ label: '临时房间不能点链接里的歌', disabled: true })
  })
})

describe('点歌', () => {
  it('成功后用服务端返回的歌名提示', async () => {
    const { enqueue, socket, notifications } = await joined()
    const link = { source: 'link', id: 'https://b23.tv/abc' } as const
    const done = enqueue(link)
    expect(socket.last('queue.add').data).toEqual({ track: link })
    socket.respond(socket.last('queue.add'), { entry: video })
    await expect(done).resolves.toBe(true)
    expect(notifications()).toMatchObject([{ tone: 'success', message: '已点歌《晴天》' }])
  })

  it('点了认得出却不能点的 B 站链接时，照原样显示服务端给的原因', async () => {
    const { enqueue, socket, notifications } = await joined()
    const done = enqueue({ source: 'link', id: 'https://www.bilibili.com/bangumi/play/ep1' })
    socket.reject(socket.last('queue.add'), 'INVALID_ARGUMENT', '哔哩哔哩的番剧不能点播')
    await expect(done).resolves.toBe(false)
    expect(notifications()).toMatchObject([{ tone: 'error', message: '哔哩哔哩的番剧不能点播' }])
  })

  it('点的不是链接时，参数错误换成通用的说法，不显示带字段路径的原文', async () => {
    const { enqueue, socket, notifications } = await joined()
    const done = enqueue({ source: 'netease', id: '' })
    socket.reject(socket.last('queue.add'), 'INVALID_ARGUMENT', 'track.id：长度不能少于 1 个字符')
    await expect(done).resolves.toBe(false)
    expect(notifications()).toMatchObject([{ tone: 'error', message: '提交的内容不符合要求，请检查后再试。' }])
  })
})
