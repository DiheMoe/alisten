import type { RoomNotice, RoomState } from './replica'
import type { ConnectionStatus, ErrorInfo, HelloResponse, Member, RequestDataMap, RequestType, ResponseDataMap, RoomEventMessage } from '@/core/realtime'
import { Connection, RequestError, roomRequests } from '@/core/realtime'
import { Emitter } from '@/shared/lib/emitter'
import { Store } from '@/shared/lib/store'
import { RoomReplica } from './replica'

interface RoomStoreState {
  roomId: string | null
  status: ConnectionStatus | null
  // 重连期间保留上一次的状态。
  room: RoomState | null
  // room 最近一次的更新来自按顺序应用的实时事件，而不是握手或 room.sync 的快照。
  live: boolean
  member: Member | null
  stopCode: number | null
}

const idle: RoomStoreState = { roomId: null, status: null, room: null, live: false, member: null, stopCode: null }

class RoomStore extends Store<RoomStoreState> {
  readonly notices = new Emitter<RoomNotice>()
  readonly errors = new Emitter<ErrorInfo>()
  readonly requests = roomRequests((type, data) => this.request(type, data))
  #connection: Connection | null = null
  #replica: RoomReplica | null = null
  #syncing: Promise<void> | null = null

  constructor() {
    super(idle)
  }

  join(roomId: string, ticket: string): void {
    this.leave()
    const connection = new Connection(roomId, ticket, {
      status: (status) => {
        if (connection === this.#connection)
          this.setState({ status })
      },
      hello: response => this.#hello(connection, response),
      event: event => this.#event(connection, event),
      error: (error) => {
        if (connection === this.#connection)
          this.errors.emit(error)
      },
      stop: code => this.#stop(connection, code),
    })
    this.#connection = connection
    this.setState({ roomId, status: 'connecting' })
    connection.start()
  }

  leave(): void {
    const connection = this.#connection
    this.#connection = null
    connection?.stop()
    this.#replica = null
    this.#syncing = null
    this.setState(idle)
  }

  reconnect(): void {
    this.#connection?.check()
  }

  request<T extends RequestType>(type: T, data: RequestDataMap[T]): Promise<ResponseDataMap[T]> {
    if (!this.#connection)
      return Promise.reject(new RequestError('还没有加入房间', null))
    return this.#connection.request(type, data)
  }

  sync(): Promise<void> {
    const replica = this.#replica
    if (this.#syncing || !replica)
      return this.#syncing ?? Promise.resolve()
    const syncing = replica.sync(() => this.request('room.sync', {}).then(({ room }) => room)).then(() => {
      if (replica === this.#replica)
        this.setState({ room: replica.state, live: false })
    }, () => {}).finally(() => {
      if (this.#syncing === syncing)
        this.#syncing = null
    })
    this.#syncing = syncing
    return syncing
  }

  #hello(connection: Connection, response: HelloResponse): void {
    if (connection !== this.#connection)
      return
    if (this.#replica)
      this.#replica.applySnapshot(response.room)
    else
      this.#replica = new RoomReplica(response.room)
    this.setState({ room: this.#replica.state, live: false, member: response.member })
  }

  #event(connection: Connection, event: RoomEventMessage): void {
    const replica = this.#replica
    if (connection !== this.#connection || !replica)
      return
    const outcome = replica.receive(event)
    if (outcome.notice)
      this.notices.emit(outcome.notice)
    if (outcome.changed)
      this.setState({ room: replica.state, live: true })
    if (outcome.sync)
      void this.sync()
  }

  #stop(connection: Connection, code: number): void {
    if (connection !== this.#connection)
      return
    this.#replica = null
    this.setState({ room: null, live: false, member: null, stopCode: code })
  }
}

export const roomStore = new RoomStore()
