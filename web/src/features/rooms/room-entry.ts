import type { RoomListing } from '@/core/realtime'
import { api, ApiError, deleteTicket, IdentityRenewedError, loadTicket, saveTicket } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { CloseCode } from '@/core/realtime'
import { roomStore } from '@/core/room'
import { notificationStore } from '@/shared/lib/notification-store'
import { Store } from '@/shared/lib/store'
import { clearLastRoom, loadLastRoom, saveLastRoom } from './last-room'

export type EntryPhase = 'idle' | 'resolving' | 'needs-ticket' | 'joining' | 'joined' | 'not-found' | 'failed'

interface EntryState {
  phase: EntryPhase
  // 房间的 UUID 或别名。
  ref: string | null
  room: RoomListing | null
}

export function joinErrorMessage(error: unknown): string {
  return error instanceof IdentityRenewedError ? '没有加入房间，再加入一次。' : errorMessage(error)
}

class RoomEntryStore extends Store<EntryState> {
  #attempt = 0
  // 加入弹窗里换票据的轮次。和 #attempt 分开，因为关掉弹窗不能打断另一个房间还在进行的进入。
  #joining = 0

  constructor() {
    super({ phase: 'idle', ref: null, room: null })
  }

  start(): void {
    roomStore.addEventListener('change', this.#onSession)
  }

  // 已经在这个房间里时什么也不做。重进会断开再连接，这次进房以来的聊天就没了。
  enter(ref: string, known?: RoomListing): Promise<void> {
    const { phase, ref: entered, room } = this.state
    if (phase === 'joined' && room && (ref === entered || ref === room.id || ref === room.slug || known?.id === room.id))
      return Promise.resolve()
    return this.#enter(ref, known)
  }

  // 用户在加入弹窗里确认之后调用。没有票据就先换取，再进入房间。返回 false 表示没有进入。
  // 换票据期间弹窗被关掉，或者已经改去别的房间时，换到的票据留着，但不再进入，失败也不再提示。
  async join(room: RoomListing, password: string): Promise<boolean> {
    const joining = ++this.#joining
    if (!loadTicket(room.id)) {
      try {
        const { ticket } = await api.createTicket(room.id, room.hasPassword ? { password } : {})
        saveTicket(room.id, ticket)
      }
      catch (error) {
        if (joining === this.#joining)
          notificationStore.error(joinErrorMessage(error))
        return false
      }
      if (joining !== this.#joining)
        return false
    }
    void this.enter(room.slug ?? room.id, room)
    return true
  }

  cancelJoin(): void {
    ++this.#joining
  }

  async #enter(ref: string, known?: RoomListing): Promise<void> {
    const attempt = ++this.#attempt
    ++this.#joining
    roomStore.leave()
    this.setState({ phase: 'resolving', ref, room: known ?? null })
    let room = known
    if (!room) {
      try {
        room = await api.getRoom(ref)
      }
      catch (error) {
        if (attempt !== this.#attempt)
          return
        if (error instanceof ApiError && error.code === 'NOT_FOUND') {
          this.#notFound()
          return
        }
        notificationStore.error(errorMessage(error))
        this.setState({ phase: 'failed' })
        return
      }
      if (attempt !== this.#attempt)
        return
    }
    const ticket = loadTicket(room.id)
    if (!ticket) {
      this.setState({ phase: 'needs-ticket', room })
      return
    }
    this.setState({ phase: 'joining', room })
    roomStore.join(room.id, ticket)
  }

  resume(): string | null {
    const roomId = loadLastRoom()
    if (roomId)
      void this.enter(roomId)
    return roomId
  }

  retry(): void {
    if (this.state.ref)
      void this.#enter(this.state.ref)
  }

  reset(): void {
    ++this.#attempt
    ++this.#joining
    roomStore.leave()
    this.setState({ phase: 'idle', ref: null, room: null })
  }

  #onSession = (): void => {
    const { phase, room } = this.state
    const session = roomStore.state
    if (!room || session.roomId !== room.id)
      return
    switch (session.stopCode) {
      case CloseCode.unauthorized:
        // 票据无效，或者房间启用、更改了密码。这时重新查询房间，再让用户输入密码。
        deleteTicket(room.id)
        this.retry()
        return
      case CloseCode.notFound:
        this.#notFound()
        return
      case CloseCode.invalidArgument:
        roomStore.leave()
        this.setState({ phase: 'failed' })
        return
    }
    if (phase === 'joining' && session.room) {
      saveLastRoom(room.id)
      this.setState({ phase: 'joined' })
    }
  }

  #notFound(): void {
    roomStore.leave()
    const lastRoom = loadLastRoom()
    if (lastRoom !== null && (lastRoom === this.state.ref || lastRoom === this.state.room?.id))
      clearLastRoom()
    this.setState({ phase: 'not-found' })
  }
}

export const roomEntryStore = new RoomEntryStore()
