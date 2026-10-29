import type { RoomListing } from '@/core/realtime'
import { api, ApiError, deleteTicket, IdentityRenewedError, loadTicket } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { CloseCode } from '@/core/realtime'
import { roomStore } from '@/core/room'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'
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

  constructor() {
    super({ phase: 'idle', ref: null, room: null })
  }

  start(): void {
    roomStore.addEventListener('change', this.#onSession)
  }

  async enter(ref: string, known?: RoomListing): Promise<void> {
    const attempt = ++this.#attempt
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
      void this.enter(this.state.ref)
  }

  reset(): void {
    ++this.#attempt
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
        // 票据无效，或房间启用、更改了密码：重新查询房间，再让用户输入密码。
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
