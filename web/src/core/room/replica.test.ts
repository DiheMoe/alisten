import type { Entry, RoomEventMessage, RoomSnapshot } from '@/core/realtime'
import { describe, expect, it } from 'vitest'
import { RoomReplica } from './replica'

const alice = { id: 'm1', name: 'alice', avatarHash: '' }

function snapshot(term: number, seq: number, patch: Partial<RoomSnapshot> = {}): RoomSnapshot {
  return {
    term,
    seq,
    room: { id: 'r1', name: '房间', description: '', kind: 'persistent' },
    playMode: 'sequential',
    nowPlaying: null,
    pending: null,
    items: [],
    members: { sessions: [], onlineCount: 0 },
    ...patch,
  }
}

function entry(id: string): Entry {
  return {
    id,
    track: { source: 'netease', id },
    meta: { title: id, artist: '', album: '', coverUrl: '', pageUrl: '', durationMs: 1000 },
    by: alice,
    auto: false,
    likes: 0,
    status: 'ready',
  }
}

function queued(term: number, seq: number, ids: string[]): RoomEventMessage {
  return { kind: 'event', type: 'queue.updated', term, seq, data: { pending: null, items: ids.map(entry) } }
}

function chat(term: number, seq: number, content: string): RoomEventMessage {
  return { kind: 'event', type: 'chat.message', term, seq, data: { member: alice, content, sentAtMs: 1 } }
}

const nothing = { changed: false, notice: null, sync: false }

describe('roomReplica', () => {
  it('依次应用连续的事件', () => {
    const replica = new RoomReplica(snapshot(3, 10))
    expect(replica.receive(queued(3, 11, ['a']))).toEqual({ changed: true, notice: null, sync: false })
    expect(replica.receive({ kind: 'event', type: 'settings.updated', term: 3, seq: 12, data: { playMode: 'random' } }).changed).toBe(true)
    expect(replica.receive({ kind: 'event', type: 'room.updated', term: 3, seq: 13, data: { name: '新名字', description: '介绍' } }).changed).toBe(true)
    expect(replica.state.items.map(e => e.id)).toEqual(['a'])
    expect(replica.state.playMode).toBe('random')
    expect(replica.state.room).toEqual({ id: 'r1', name: '新名字', description: '介绍', kind: 'persistent' })
  })

  it('丢弃旧任期和重复的事件', () => {
    const replica = new RoomReplica(snapshot(3, 10))
    expect(replica.receive(queued(2, 11, ['old']))).toEqual(nothing)
    expect(replica.receive(queued(3, 10, ['dup']))).toEqual(nothing)
    expect(replica.state.items).toEqual([])
  })

  it('seq 不连续或任期更大时请求快照，等待期间只请求一次', () => {
    const gap = new RoomReplica(snapshot(3, 10))
    expect(gap.receive(queued(3, 12, ['a']))).toEqual({ changed: false, notice: null, sync: true })
    expect(gap.receive(queued(3, 13, ['b']))).toEqual(nothing)
    expect(gap.state.items).toEqual([])

    const newer = new RoomReplica(snapshot(3, 10))
    expect(newer.receive(queued(4, 1, ['a'])).sync).toBe(true)
  })

  it('等待快照期间照常显示通知，并按编号去重', () => {
    const replica = new RoomReplica(snapshot(3, 10))
    expect(replica.receive(chat(3, 12, '第一条')).notice?.data).toMatchObject({ content: '第一条' })
    expect(replica.receive(chat(3, 12, '第一条')).notice).toBeNull()
    expect(replica.receive(chat(3, 13, '第二条')).notice?.data).toMatchObject({ content: '第二条' })
    expect(replica.receive(chat(2, 20, '旧任期')).notice).toBeNull()
  })

  it('以快照为准，之后接着快照的编号应用事件', () => {
    const replica = new RoomReplica(snapshot(3, 10))
    replica.receive(chat(3, 12, '等待时收到'))
    replica.applySnapshot(snapshot(3, 11, { items: [entry('a')] }))
    expect(replica.state.items.map(e => e.id)).toEqual(['a'])
    expect(replica.receive(chat(3, 12, '等待时收到'))).toEqual({ changed: false, notice: null, sync: false })
    expect(replica.receive(queued(3, 13, ['a', 'b'])).changed).toBe(true)
    expect(replica.state.items.map(e => e.id)).toEqual(['a', 'b'])
  })

  it('连续的通知事件推进编号', () => {
    const replica = new RoomReplica(snapshot(3, 10))
    expect(replica.receive(chat(3, 11, '你好')).notice?.data).toMatchObject({ content: '你好' })
    expect(replica.receive(queued(3, 12, ['a'])).changed).toBe(true)
  })

  it('获取快照失败后，下一个不连续的事件再次请求', async () => {
    const replica = new RoomReplica(snapshot(3, 10))
    expect(replica.receive(queued(3, 12, ['a'])).sync).toBe(true)
    await expect(replica.sync(() => Promise.reject(new Error('与房间的连接已断开')))).rejects.toThrow('与房间的连接已断开')
    expect(replica.receive(queued(3, 13, ['b'])).sync).toBe(true)
  })

  it('请求快照期间丢弃状态事件，快照到达后接着它的编号应用', async () => {
    const replica = new RoomReplica(snapshot(3, 10))
    let respond: (room: RoomSnapshot) => void = () => {}
    const syncing = replica.sync(() => new Promise((resolve) => {
      respond = resolve
    }))
    expect(replica.receive(queued(3, 11, ['a']))).toEqual(nothing)
    respond(snapshot(3, 11, { items: [entry('b')] }))
    await syncing
    expect(replica.state.items.map(e => e.id)).toEqual(['b'])
    expect(replica.receive(queued(3, 12, ['b', 'c'])).changed).toBe(true)
  })
})
