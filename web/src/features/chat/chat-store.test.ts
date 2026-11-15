import type { Entry, HelloResponse, Member, RoomActivityEvent, RoomEventMessage } from '@/core/realtime'
import type { FakeSocket } from '@/core/realtime/fake-socket'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { sockets, stubBrowser } from '@/core/realtime/fake-socket'

const term = 2
const abnormalClosure = 1006
const localNowMs = Date.UTC(2026, 0, 1)
const serverAheadMs = 90_000
const me: Member = { id: 'm1', name: 'alice', avatarHash: '' }
const bob: Member = { id: 'm2', name: 'bob', avatarHash: '' }
const prepareFailed = '《付费视频》没能准备好：这个视频需要付费'

let seq: number

function lastSocket(): FakeSocket {
  const socket = sockets.at(-1)
  if (!socket)
    throw new Error('还没有建立连接')
  return socket
}

function hello(roomId: string): HelloResponse {
  return {
    member: me,
    room: {
      term,
      seq,
      room: { id: roomId, name: '房间', description: '', kind: 'persistent' },
      playMode: 'sequential',
      nowPlaying: null,
      pending: null,
      items: [],
      members: { sessions: [], onlineCount: 0 },
    },
    serverTimeMs: 0,
  }
}

function entry(by: Member | null): Entry {
  return {
    id: 'e1',
    track: { source: 'bilibili', id: 'BV1xx411c7mD' },
    meta: { title: '付费视频', artist: '', album: '', coverUrl: '', pageUrl: '', durationMs: 0 },
    by,
    auto: by === null,
    likes: 0,
    status: 'preparing',
  }
}

function chat(content: string, sentAtMs: number): RoomEventMessage {
  return { kind: 'event', type: 'chat.message', term, seq: ++seq, data: { member: bob, content, sentAtMs } }
}

function activity(data: RoomActivityEvent): RoomEventMessage {
  return { kind: 'event', type: 'room.activity', term, seq: ++seq, data }
}

const flush = () => vi.advanceTimersByTimeAsync(0)

async function handshake(roomId: string): Promise<FakeSocket> {
  const socket = lastSocket()
  socket.open()
  socket.respond(socket.last('hello'), hello(roomId))
  await flush()
  return socket
}

async function started() {
  const { chatStore } = await import('./chat-store')
  const { roomStore } = await import('@/core/room')
  const { notificationStore } = await import('@/shared/lib/notification-store')
  chatStore.start()
  const join = (roomId: string) => {
    roomStore.join(roomId, 't1')
    return handshake(roomId)
  }
  return { chatStore, roomStore, notificationStore, join }
}

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers({ now: localNowMs })
  seq = 0
  stubBrowser()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('chatStore', () => {
  it('聊天消息带着发送人，时间用服务端给的发送时间', async () => {
    const { chatStore, join } = await started()
    const socket = await join('r1')
    socket.receive(chat('你好', 1234))
    expect(chatStore.state.items).toEqual([{ kind: 'message', key: '2:1', member: bob, text: '你好', atMs: 1234 }])
  })

  it('房间动态按收到时的服务器时间显示，时钟还没校准时用本机时间', async () => {
    const { chatStore, join } = await started()
    const socket = await join('r1')
    socket.receive(activity({ kind: 'added', member: bob, entry: entry(bob), message: '点了《付费视频》' }))
    expect(chatStore.state.items[0]).toEqual({ kind: 'activity', key: '2:1', member: bob, text: '点了《付费视频》', atMs: localNowMs })

    socket.respond(socket.last('ping'), { serverTimeMs: localNowMs + serverAheadMs })
    await vi.advanceTimersByTimeAsync(2000)
    socket.receive(activity({ kind: 'failed', entry: entry(bob), message: prepareFailed }))
    expect(chatStore.state.items[1]).toEqual({ kind: 'activity', key: '2:2', member: null, text: prepareFailed, atMs: localNowMs + serverAheadMs + 2000 })
  })

  it('自己点的歌准备失败时，另外用通知提醒', async () => {
    const { notificationStore, join } = await started()
    const socket = await join('r1')
    socket.receive(activity({ kind: 'failed', entry: entry(me), message: prepareFailed }))
    expect(notificationStore.state.notifications).toMatchObject([{ tone: 'error', message: prepareFailed }])
  })

  it.each<[string, RoomActivityEvent]>([
    ['别人点的歌准备失败', { kind: 'failed', entry: entry(bob), message: prepareFailed }],
    ['AutoDJ 加入的歌准备失败', { kind: 'failed', entry: entry(null), message: prepareFailed }],
    ['自己点歌成功', { kind: 'added', member: me, entry: entry(me), message: '点了《付费视频》' }],
  ])('%s时只显示在聊天里，不另外提醒', async (_, data) => {
    const { chatStore, notificationStore, join } = await started()
    const socket = await join('r1')
    socket.receive(activity(data))
    expect(chatStore.state.items).toHaveLength(1)
    expect(notificationStore.state.notifications).toEqual([])
  })

  it('换房间时清空', async () => {
    const { chatStore, join } = await started()
    const socket = await join('r1')
    socket.receive(chat('你好', 1))
    expect(chatStore.state.items).toHaveLength(1)

    await join('r2')
    expect(chatStore.state.items).toEqual([])
  })

  it('还在同一个房间时，房间状态变化和断线重连都不清空', async () => {
    const { chatStore, roomStore, join } = await started()
    const socket = await join('r1')
    socket.receive(chat('你好', 1))
    socket.receive({ kind: 'event', type: 'settings.updated', term, seq: ++seq, data: { playMode: 'random' } } satisfies RoomEventMessage)
    expect(roomStore.state.room?.playMode).toBe('random')

    socket.lose(abnormalClosure)
    roomStore.reconnect()
    const reconnected = await handshake('r1')
    reconnected.receive(chat('回来了', 2))
    expect(chatStore.state.items.map(item => item.text)).toEqual(['你好', '回来了'])
  })
})
