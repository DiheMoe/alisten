import type { RoomEventMessage, RoomSnapshot } from '@/core/realtime'

export type RoomState = Omit<RoomSnapshot, 'term' | 'seq'>

export type RoomNotice = RoomEventMessage<'chat.message' | 'room.activity'>

export interface Outcome {
  readonly changed: boolean
  readonly notice: RoomNotice | null
  readonly sync: boolean
}

const ignored: Outcome = { changed: false, notice: null, sync: false }

function isNotice(event: RoomEventMessage): event is RoomNotice {
  return event.type === 'chat.message' || event.type === 'room.activity'
}

function after(term: number, seq: number, thanTerm: number, thanSeq: number): boolean {
  return term > thanTerm || (term === thanTerm && seq > thanSeq)
}

export class RoomReplica {
  state: RoomState
  #term = 0
  #seq = 0
  #waiting = false
  #noticeTerm = 0
  #noticeSeq = 0

  constructor(snapshot: RoomSnapshot) {
    this.state = this.#apply(snapshot)
  }

  receive(event: RoomEventMessage): Outcome {
    if (event.term < this.#term)
      return ignored
    if (!this.#waiting) {
      if (event.term === this.#term && event.seq <= this.#seq)
        return ignored
      if (event.term === this.#term && event.seq === this.#seq + 1) {
        this.#seq = event.seq
        if (isNotice(event))
          return { changed: false, notice: this.#notice(event), sync: false }
        this.#update(event)
        return { changed: true, notice: null, sync: false }
      }
      this.#waiting = true
      return { changed: false, notice: isNotice(event) ? this.#notice(event) : null, sync: true }
    }
    // 快照不包含聊天，等待期间通知照常显示；状态事件以快照为准。
    return { changed: false, notice: isNotice(event) ? this.#notice(event) : null, sync: false }
  }

  // 请求在途期间收到的状态事件以快照为准，所以先进入等待，再发出请求。
  async sync(fetchSnapshot: () => Promise<RoomSnapshot>): Promise<void> {
    this.#waiting = true
    try {
      this.applySnapshot(await fetchSnapshot())
    }
    finally {
      this.#waiting = false
    }
  }

  applySnapshot(snapshot: RoomSnapshot): void {
    this.state = this.#apply(snapshot)
    this.#waiting = false
  }

  #apply(snapshot: RoomSnapshot): RoomState {
    const { term, seq, ...state } = snapshot
    this.#term = term
    this.#seq = seq
    if (after(term, seq, this.#noticeTerm, this.#noticeSeq)) {
      this.#noticeTerm = term
      this.#noticeSeq = seq
    }
    return state
  }

  #notice(event: RoomNotice): RoomNotice | null {
    if (!after(event.term, event.seq, this.#noticeTerm, this.#noticeSeq))
      return null
    this.#noticeTerm = event.term
    this.#noticeSeq = event.seq
    return event
  }

  #update(event: Exclude<RoomEventMessage, RoomNotice>): void {
    switch (event.type) {
      case 'playback.updated':
        this.state = { ...this.state, nowPlaying: event.data.nowPlaying }
        break
      case 'queue.updated':
        this.state = { ...this.state, pending: event.data.pending, items: event.data.items }
        break
      case 'members.updated':
        this.state = { ...this.state, members: event.data }
        break
      case 'settings.updated':
        this.state = { ...this.state, playMode: event.data.playMode }
        break
      case 'room.updated':
        this.state = { ...this.state, room: { ...this.state.room, name: event.data.name, description: event.data.description } }
        break
    }
  }
}
