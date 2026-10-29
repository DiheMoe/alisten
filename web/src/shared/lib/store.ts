export interface ExternalStore<T> extends EventTarget {
  readonly state: T
}

// 状态整体替换：订阅方可以按引用比较字段，判断它是否变化。
export class Store<T extends object> extends EventTarget implements ExternalStore<T> {
  #state: T

  constructor(initial: T) {
    super()
    this.#state = initial
  }

  get state(): T {
    return this.#state
  }

  protected setState(patch: Partial<T>): void {
    const keys = Object.keys(patch) as (keyof T)[]
    if (keys.every(key => Object.is(patch[key], this.#state[key])))
      return
    this.#state = { ...this.#state, ...patch }
    this.dispatchEvent(new Event('change'))
  }
}
