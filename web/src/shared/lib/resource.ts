import { Store } from './store'

export type ResourceState<T> = { status: 'loading' } | { status: 'loaded', value: T } | { status: 'failed', error: string }

const loading: ResourceState<never> = { status: 'loading' }

// 只有最后发起的读取能写入结果，之前发起的读取都作废。request 失败时抛出 Error，message 是给用户看的原因。
// 打开之后 read、refresh 和 set 才生效。关闭之后它们都不再生效，直到再次打开。
export class Resource<T> extends Store<{ current: ResourceState<T> }> {
  #reads = 0
  #open = false

  constructor() {
    super({ current: loading })
  }

  open(request: () => Promise<T>): void {
    this.#open = true
    this.read(request)
  }

  // 还没返回的读取也作废。
  close(): void {
    this.#open = false
    ++this.#reads
  }

  read(request: () => Promise<T>): void {
    if (!this.#open)
      return
    this.setState({ current: loading })
    this.#start(request)
  }

  // 已经读到值时，读取期间继续显示这个值。
  refresh(request: () => Promise<T>): void {
    if (!this.#open)
      return
    if (this.state.current.status !== 'loaded')
      this.setState({ current: loading })
    this.#start(request)
  }

  // 还没返回的读取作废，因为它们读到的可能是改写之前的值。
  set(value: T): void {
    if (!this.#open)
      return
    ++this.#reads
    this.setState({ current: { status: 'loaded', value } })
  }

  #start(request: () => Promise<T>): void {
    const read = ++this.#reads
    request().then((value) => {
      if (read === this.#reads)
        this.setState({ current: { status: 'loaded', value } })
    }, (error: Error) => {
      if (read === this.#reads)
        this.setState({ current: { status: 'failed', error: error.message } })
    })
  }
}
