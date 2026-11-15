import type { RoomNotice } from './replica'
import type { ErrorInfo, RoomEventMessage, RoomSnapshot } from '@/core/realtime'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CloseCode } from '@/core/realtime'
import { FakeSocket, socketAt, stubBrowser } from '@/core/realtime/fake-socket'

const abnormalClosure = 1006
const alice = { id: 'm1', name: 'alice', avatarHash: '' }

// 用房间名区分每一份状态，因为快照和 room.updated 事件都带着房间名。
function snapshot(term: number, seq: number, name: string): RoomSnapshot {
  return {
    term,
    seq,
    room: { id: 'r1', name, description: '', kind: 'persistent' },
    playMode: 'sequential',
    nowPlaying: null,
    pending: null,
    items: [],
    members: { sessions: [], onlineCount: 0 },
  }
}

function renamed(term: number, seq: number, name: string): RoomEventMessage {
  return { kind: 'event', type: 'room.updated', term, seq, data: { name, description: '' } }
}

function chat(term: number, seq: number, content: string): RoomEventMessage {
  return { kind: 'event', type: 'chat.message', term, seq, data: { member: alice, content, sentAtMs: 1 } }
}

const flush = () => vi.advanceTimersByTimeAsync(0)

async function handshake(socket: FakeSocket, room: RoomSnapshot): Promise<void> {
  socket.open()
  socket.respond(socket.last('hello'), { member: alice, room, serverTimeMs: 0 })
  await flush()
}

async function load() {
  const { roomStore } = await import('./room-store')
  const name = () => roomStore.state.room?.room.name
  return { roomStore, name }
}

async function joined() {
  const loaded = await load()
  loaded.roomStore.join('r1', 't1')
  await handshake(socketAt(0), snapshot(1, 0, '握手时'))
  return loaded
}

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
  vi.spyOn(Math, 'random').mockReturnValue(0.5)
  stubBrowser()
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('加入房间', () => {
  it('加入后先是 connecting，握手成功才有房间和成员，这时的房间还不算 live', async () => {
    const { roomStore } = await load()
    roomStore.join('r1', 't1')
    expect(roomStore.state).toEqual({ roomId: 'r1', status: 'connecting', room: null, live: false, member: null, stopCode: null })

    const socket = socketAt(0)
    socket.open()
    expect(socket.last('hello').data).toMatchObject({ roomId: 'r1', ticket: 't1' })
    socket.respond(socket.last('hello'), { member: alice, room: snapshot(1, 0, '握手时'), serverTimeMs: 0 })
    await flush()
    expect(roomStore.state).toMatchObject({ roomId: 'r1', status: 'open', room: { room: { name: '握手时' } }, live: false, member: alice, stopCode: null })
  })

  it('加入另一个房间时先离开当前房间，不带着它的状态', async () => {
    const { roomStore } = await joined()
    socketAt(0).receive(renamed(1, 1, '第一个房间'))

    roomStore.join('r2', 't2')
    expect(socketAt(0).readyState).toBe(FakeSocket.CLOSED)
    expect(roomStore.state).toEqual({ roomId: 'r2', status: 'connecting', room: null, live: false, member: null, stopCode: null })
  })

  it('离开之后的请求直接失败，提示还没有加入房间', async () => {
    const { roomStore } = await joined()
    roomStore.leave()
    await expect(roomStore.requests.chatSend({ content: '你好' })).rejects.toMatchObject({ name: 'RequestError', message: '还没有加入房间', code: null })
  })
})

describe('房间事件', () => {
  it('按编号顺序到达的事件更新房间，并标记为 live', async () => {
    const { roomStore, name } = await joined()
    socketAt(0).receive(renamed(1, 1, '改名之后'))
    expect(name()).toBe('改名之后')
    expect(roomStore.state.live).toBe(true)
  })

  it('聊天消息交给 notices，房间状态不变', async () => {
    const { roomStore } = await joined()
    const notices: RoomNotice[] = []
    roomStore.notices.on(notice => notices.push(notice))
    const changed = vi.fn()
    roomStore.addEventListener('change', changed)

    socketAt(0).receive(chat(1, 1, '你好'))
    expect(notices).toEqual([chat(1, 1, '你好')])
    expect(changed).not.toHaveBeenCalled()
    expect(roomStore.state.live).toBe(false)
  })

  it('事件编号不连续时补一次快照，快照到达后以它为准，房间不再算 live', async () => {
    const { roomStore, name } = await joined()
    const socket = socketAt(0)
    socket.receive(renamed(1, 1, '第 1 号事件'))

    socket.receive(renamed(1, 3, '第 3 号事件'))
    socket.receive(renamed(1, 4, '第 4 号事件'))
    expect(socket.count('room.sync')).toBe(1)
    expect(roomStore.state).toMatchObject({ live: true, room: { room: { name: '第 1 号事件' } } })

    socket.respond(socket.last('room.sync'), { room: snapshot(1, 4, '快照') })
    await flush()
    expect(roomStore.state).toMatchObject({ live: false, room: { room: { name: '快照' } } })

    socket.receive(renamed(1, 5, '第 5 号事件'))
    expect(name()).toBe('第 5 号事件')
    expect(socket.count('room.sync')).toBe(1)
  })
})

describe('补快照', () => {
  it('还没有握手时直接完成，不发请求', async () => {
    const { roomStore } = await load()
    roomStore.join('r1', 't1')
    socketAt(0).open()
    await expect(roomStore.sync()).resolves.toBeUndefined()
    expect(socketAt(0).count('room.sync')).toBe(0)
  })

  it('同时发起的几次共用一个请求，都等到快照写入', async () => {
    const { roomStore, name } = await joined()
    const socket = socketAt(0)
    const first = roomStore.sync()
    const second = roomStore.sync()
    expect(socket.count('room.sync')).toBe(1)

    socket.respond(socket.last('room.sync'), { room: snapshot(1, 2, '快照') })
    await Promise.all([first, second])
    expect(name()).toBe('快照')
  })

  it('途中断线时不抛出，房间保持原样，重连之后还能再补', async () => {
    const { roomStore, name } = await joined()
    const syncing = roomStore.sync()
    socketAt(0).lose(abnormalClosure)
    await expect(syncing).resolves.toBeUndefined()
    expect(name()).toBe('握手时')

    await vi.advanceTimersByTimeAsync(3000)
    await handshake(socketAt(1), snapshot(1, 2, '重连之后'))
    socketAt(1).receive(renamed(1, 4, '第 4 号事件'))
    expect(socketAt(1).count('room.sync')).toBe(1)
  })
})

describe('断线和关闭', () => {
  it('断线重连期间保留房间和成员，重新握手后以新快照为准，之后接着它的编号应用事件', async () => {
    const { roomStore, name } = await joined()
    socketAt(0).receive(renamed(1, 1, '断线之前'))

    socketAt(0).lose(abnormalClosure)
    expect(roomStore.state).toMatchObject({ status: 'reconnecting', live: true, member: alice, room: { room: { name: '断线之前' } } })

    await vi.advanceTimersByTimeAsync(3000)
    await handshake(socketAt(1), snapshot(2, 7, '重连之后'))
    expect(roomStore.state).toMatchObject({ status: 'open', live: false, room: { room: { name: '重连之后' } } })

    socketAt(1).receive(renamed(2, 8, '第 8 号事件'))
    expect(name()).toBe('第 8 号事件')
    expect(socketAt(1).count('room.sync')).toBe(0)
  })

  it('服务端报告错误并以停止码关闭时，把错误交给 errors，清掉房间和成员，记下关闭码', async () => {
    const { roomStore } = await joined()
    const errors: ErrorInfo[] = []
    roomStore.errors.on(error => errors.push(error))

    socketAt(0).receive({ kind: 'event', type: 'error', data: { code: 'NOT_FOUND', message: '房间已经关闭' } })
    socketAt(0).lose(CloseCode.notFound)
    expect(errors).toEqual([{ code: 'NOT_FOUND', message: '房间已经关闭' }])
    expect(roomStore.state).toEqual({ roomId: 'r1', status: 'stopped', room: null, live: false, member: null, stopCode: CloseCode.notFound })
  })
})
