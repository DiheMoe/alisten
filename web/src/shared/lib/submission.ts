// 同一时间只进行一次提交，提交期间再次提交会被忽略。提交结束时，不论成功还是失败都复位。
// 关闭之后不再通知 onBusy，直到再次打开。
export class Submission {
  #busy = false
  #open = false
  readonly #onBusy: (busy: boolean) => void

  constructor(onBusy: (busy: boolean) => void) {
    this.#onBusy = onBusy
  }

  open(): void {
    this.#open = true
  }

  close(): void {
    this.#open = false
  }

  async run(task: () => Promise<void>): Promise<void> {
    if (this.#busy)
      return
    this.#set(true)
    try {
      await task()
    }
    finally {
      this.#set(false)
    }
  }

  #set(busy: boolean): void {
    this.#busy = busy
    if (this.#open)
      this.#onBusy(busy)
  }
}
