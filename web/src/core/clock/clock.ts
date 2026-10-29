// 每 5 秒采集一个样本，12 个样本约覆盖 1 分钟。
const sampleLimit = 12

interface Sample {
  offsetMs: number
  roundTripMs: number
}

export class Clock extends EventTarget {
  readonly #localNow: () => number
  #samples: Sample[] = []
  #resetAtMs = Number.NEGATIVE_INFINITY
  #offsetMs: number | null = null

  constructor(localNow: () => number) {
    super()
    this.#localNow = localNow
  }

  get offsetMs(): number | null {
    return this.#offsetMs
  }

  now(): number {
    return this.#localNow()
  }

  serverNow(): number | null {
    return this.#offsetMs === null ? null : this.#localNow() + this.#offsetMs
  }

  addSample(sentAtMs: number, receivedAtMs: number, serverTimeMs: number): void {
    // 清空样本之前发出的 ping 跨越了清空时刻，其往返时间不可信。
    if (sentAtMs < this.#resetAtMs)
      return
    this.#samples = [
      ...this.#samples,
      { offsetMs: serverTimeMs - (sentAtMs + receivedAtMs) / 2, roundTripMs: receivedAtMs - sentAtMs },
    ].slice(-sampleLimit)
    const best = this.#samples.reduce((a, b) => (b.roundTripMs < a.roundTripMs ? b : a))
    this.#setOffset(best.offsetMs)
  }

  reset(): void {
    this.#samples = []
    this.#resetAtMs = this.#localNow()
    this.#setOffset(null)
  }

  #setOffset(offsetMs: number | null): void {
    if (offsetMs === this.#offsetMs)
      return
    this.#offsetMs = offsetMs
    this.dispatchEvent(new Event('change'))
  }
}

// performance.now() 不受系统校时影响。
export const clock = new Clock(() => performance.timeOrigin + performance.now())
