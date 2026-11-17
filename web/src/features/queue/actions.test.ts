import type { HelloResponse, Member } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { sockets, stubBrowser } from '@/core/realtime/fake-socket'

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
    items: [],
    members: { sessions: [], onlineCount: 0 },
  },
  serverTimeMs: 0,
}

async function joined() {
  const actions = await import('./actions')
  const { roomStore } = await import('@/core/room')
  const { notificationStore } = await import('@/shared/lib/notification-store')
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
