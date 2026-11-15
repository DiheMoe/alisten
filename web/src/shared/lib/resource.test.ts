import { describe, expect, it } from 'vitest'
import { Resource } from './resource'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

const settle = () => new Promise(resolve => setTimeout(resolve, 0))

async function loaded(value: string): Promise<Resource<string>> {
  const resource = new Resource<string>()
  resource.open(() => Promise.resolve(value))
  await settle()
  return resource
}

async function failed(): Promise<Resource<string>> {
  const resource = new Resource<string>()
  resource.open(() => Promise.reject(new Error('网络不通')))
  await settle()
  return resource
}

describe('读取资源', () => {
  it('读取期间是读取中，读到之后是读到的值', async () => {
    const resource = new Resource<string>()
    const pending = deferred<string>()
    resource.open(() => pending.promise)
    expect(resource.state.current).toEqual({ status: 'loading' })
    pending.resolve('歌单')
    await settle()
    expect(resource.state.current).toEqual({ status: 'loaded', value: '歌单' })
  })

  it('失败时带上 Error 的 message', async () => {
    const resource = await failed()
    expect(resource.state.current).toEqual({ status: 'failed', error: '网络不通' })
  })

  it('结果以最后一次读取为准，先发起的读取晚回来也不覆盖', async () => {
    const resource = new Resource<string>()
    const first = deferred<string>()
    const second = deferred<string>()
    resource.open(() => first.promise)
    resource.read(() => second.promise)
    second.resolve('新')
    await settle()
    first.resolve('旧')
    await settle()
    expect(resource.state.current).toEqual({ status: 'loaded', value: '新' })
  })

  it('先发起的读取先回来时也作废，仍然等最后一次读取', async () => {
    const resource = new Resource<string>()
    const first = deferred<string>()
    const second = deferred<string>()
    resource.open(() => first.promise)
    resource.read(() => second.promise)
    first.resolve('旧')
    await settle()
    expect(resource.state.current).toEqual({ status: 'loading' })
    second.reject(new Error('网络不通'))
    await settle()
    expect(resource.state.current).toEqual({ status: 'failed', error: '网络不通' })
  })

  it('作废的读取失败了也不写入', async () => {
    const resource = new Resource<string>()
    const first = deferred<string>()
    resource.open(() => first.promise)
    resource.read(() => Promise.resolve('新'))
    first.reject(new Error('网络不通'))
    await settle()
    expect(resource.state.current).toEqual({ status: 'loaded', value: '新' })
  })

  it('失败之后重试，先回到读取中，再读到值', async () => {
    const resource = await failed()
    const retry = deferred<string>()
    resource.read(() => retry.promise)
    expect(resource.state.current).toEqual({ status: 'loading' })
    retry.resolve('歌单')
    await settle()
    expect(resource.state.current).toEqual({ status: 'loaded', value: '歌单' })
  })

  it('读到值之后重新读取，也先回到读取中', async () => {
    const resource = await loaded('旧')
    resource.read(() => new Promise(() => {}))
    expect(resource.state.current).toEqual({ status: 'loading' })
  })

  it('refresh 期间保留读到的值，返回后换成新值', async () => {
    const resource = await loaded('旧')
    const pending = deferred<string>()
    resource.refresh(() => pending.promise)
    expect(resource.state.current).toEqual({ status: 'loaded', value: '旧' })
    pending.resolve('新')
    await settle()
    expect(resource.state.current).toEqual({ status: 'loaded', value: '新' })
  })

  it('还没读到值时，refresh 回到读取中', async () => {
    const resource = await failed()
    resource.refresh(() => new Promise(() => {}))
    expect(resource.state.current).toEqual({ status: 'loading' })
  })

  it('set 改写读到的值，还没返回的读取作废', async () => {
    const resource = new Resource<string>()
    const pending = deferred<string>()
    resource.open(() => pending.promise)
    resource.set('本地')
    pending.resolve('旧')
    await settle()
    expect(resource.state.current).toEqual({ status: 'loaded', value: '本地' })
  })

  it('关闭之后，还没返回的读取不再写入', async () => {
    const resource = new Resource<string>()
    const pending = deferred<string>()
    resource.open(() => pending.promise)
    resource.close()
    pending.resolve('歌单')
    await settle()
    expect(resource.state.current).toEqual({ status: 'loading' })
  })

  it('关闭之后不再发起读取，也不再改写', async () => {
    const resource = await failed()
    resource.close()
    let requested = false
    const request = () => {
      requested = true
      return Promise.resolve('歌单')
    }
    resource.read(request)
    resource.refresh(request)
    resource.set('本地')
    await settle()
    expect(requested).toBe(false)
    expect(resource.state.current).toEqual({ status: 'failed', error: '网络不通' })
  })

  it('关闭之后再次打开，照常读取', async () => {
    const resource = new Resource<string>()
    resource.open(() => new Promise(() => {}))
    resource.close()
    resource.open(() => Promise.resolve('歌单'))
    await settle()
    expect(resource.state.current).toEqual({ status: 'loaded', value: '歌单' })
  })
})
