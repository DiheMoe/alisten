import { describe, expect, it } from 'vitest'
import { Submission } from './submission'

function opened() {
  const changes: boolean[] = []
  const submission = new Submission(busy => changes.push(busy))
  submission.open()
  return { submission, changes }
}

function deferred() {
  let resolve!: () => void
  let reject!: (error: Error) => void
  const promise = new Promise<void>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('提交', () => {
  it('提交期间是提交中，成功之后复位', async () => {
    const { submission, changes } = opened()
    const task = deferred()
    const running = submission.run(() => task.promise)
    expect(changes).toEqual([true])
    task.resolve()
    await running
    expect(changes).toEqual([true, false])
  })

  it('失败时也复位，并把错误交给调用方', async () => {
    const { submission, changes } = opened()
    await expect(submission.run(() => Promise.reject(new Error('网络不通')))).rejects.toThrow('网络不通')
    expect(changes).toEqual([true, false])
  })

  it('提交期间再次提交会被忽略', async () => {
    const { submission, changes } = opened()
    const task = deferred()
    let runs = 0
    const first = submission.run(() => {
      runs++
      return task.promise
    })
    await submission.run(async () => {
      runs++
    })
    expect(runs).toBe(1)
    task.resolve()
    await first
    expect(changes).toEqual([true, false])
  })

  it('上一次提交结束之后可以再提交', async () => {
    const { submission, changes } = opened()
    await submission.run(() => Promise.reject(new Error('网络不通'))).catch(() => {})
    let ran = false
    await submission.run(async () => {
      ran = true
    })
    expect(ran).toBe(true)
    expect(changes).toEqual([true, false, true, false])
  })

  it('关闭之后提交结束也不再通知', async () => {
    const { submission, changes } = opened()
    const task = deferred()
    const running = submission.run(() => task.promise)
    submission.close()
    task.resolve()
    await running
    expect(changes).toEqual([true])
  })

  it('关闭期间结束的提交，再次打开之后也不挡住新的提交', async () => {
    const { submission, changes } = opened()
    const task = deferred()
    const running = submission.run(() => task.promise)
    submission.close()
    task.resolve()
    await running
    submission.open()
    let ran = false
    await submission.run(async () => {
      ran = true
    })
    expect(ran).toBe(true)
    expect(changes).toEqual([true, true, false])
  })
})
