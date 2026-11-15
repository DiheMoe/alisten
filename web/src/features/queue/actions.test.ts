import type { Entry, HelloResponse, Member } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { sockets, stubBrowser } from '@/core/realtime/fake-socket'

const me: Member = { id: 'm1', name: 'alice', avatarHash: '' }

const hello: HelloResponse = {
  member: me,
  room: {
    term: 1,
    seq: 0,
    room: { id: 'r1', name: '房间', description: '', kind: 'persistent' },
    playMode: 'sequential',
    nowPlaying: null,
    pending: null,
    items: [],
    members: { sessions: [], onlineCount: 0 },
  },
  serverTimeMs: 0,
}

// 服务端把 B 站的链接转成 B 站音源的曲目，歌名只在服务端的响应里。
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
  const actions = await import('./actions')
  const { roomStore } = await import('@/core/room')
  const { notificationStore } = await import('@/shared/ui')
  roomStore.join('r1', 't1')
  const socket = sockets.at(-1)
  if (!socket)
    throw new Error('还没有建立连接')
  socket.open()
  socket.respond(socket.last('hello'), hello)
  await vi.advanceTimersByTimeAsync(0)
  return { ...actions, socket, notifications: () => notificationStore.state.notifications }
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

describe('投票切歌', () => {
  it.each([
    ['票数还不够时提示当前的票数', { votes: 1, required: 2, skipped: false }, '已投票切歌（1/2）'],
    ['票数够了时提示已切歌', { votes: 2, required: 2, skipped: true }, '已切歌'],
  ])('%s', async (_, response, message) => {
    const { voteSkip, socket, notifications } = await joined()
    const done = voteSkip()
    socket.respond(socket.last('playback.voteSkip'), response)
    await done
    expect(notifications()).toMatchObject([{ tone: 'success', message }])
  })
})

describe('播放列表上的其他操作', () => {
  it('切换播放模式成功后不提示', async () => {
    const { setPlayMode, socket, notifications } = await joined()
    const done = setPlayMode('random')
    socket.respond(socket.last('settings.setPlayMode'))
    await done
    expect(notifications()).toEqual([])
  })

  it('请求失败时用通知显示原因，不向调用方抛出', async () => {
    const { removeEntry, socket, notifications } = await joined()
    const done = removeEntry('e1', '晴天')
    socket.reject(socket.last('queue.remove'), 'NOT_FOUND', '这首歌已经不在播放列表里了。')
    await expect(done).resolves.toBeUndefined()
    expect(notifications()).toMatchObject([{ tone: 'error', message: '这首歌已经不在播放列表里了。' }])
  })
})
