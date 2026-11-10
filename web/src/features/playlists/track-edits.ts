// 详情页对曲目的修改（移动、移出）先在本地改好再请求。请求按发起的先后逐个发出，每次都基于上一次修改之后的顺序。
// 一次失败之后，排在后面的修改都不再发出，由失败的那次修改负责回滚，列表回到这次修改之前的样子。
// 修改进行中读到的列表可能是修改之前的，不能用。最后一个修改结束之后再读一次，以服务端为准。
export class TrackEdits {
  readonly #reread: () => void
  #tail: Promise<void> = Promise.resolve()
  #generation = 0
  #pending = 0
  #started = 0

  constructor(reread: () => void) {
    this.#reread = reread
  }

  // request 失败时返回 false，不抛出。
  run(request: () => Promise<boolean>, rollback: () => void): Promise<void> {
    const generation = this.#generation
    this.#pending++
    this.#started++
    const done = this.#tail.then(async () => {
      if (this.#generation !== generation || await request())
        return
      this.#generation++
      rollback()
    }).finally(() => {
      if (--this.#pending === 0)
        this.#reread()
    })
    this.#tail = done
    return done
  }

  // 读取开始时调用，返回的函数在读取返回时判断读到的列表能不能用。
  read(): () => boolean {
    const idle = this.#pending === 0
    const started = this.#started
    return () => idle && this.#started === started
  }
}
