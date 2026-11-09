import { describe, expect, it } from 'vitest'
import { TrackEdits } from './track-edits'

// 由测试决定每个请求何时完成、成功与否。
function requests() {
  const pending: ((ok: boolean) => void)[] = []
  const sent: string[] = []
  const request = (name: string) => () => {
    sent.push(name)
    return new Promise<boolean>(resolve => pending.push(resolve))
  }
  const settle = async (ok: boolean) => {
    await flush()
    pending.shift()!(ok)
    await flush()
  }
  return { request, sent, settle }
}

function flush() {
  return new Promise(resolve => setTimeout(resolve, 0))
}

function setup() {
  let rereads = 0
  const edits = new TrackEdits(() => rereads++)
  return { edits, rereads: () => rereads }
}

describe('trackEdits', () => {
  it('没有修改时读到的结果可以用', () => {
    const { edits } = setup()
    const usable = edits.read()
    expect(usable()).toBe(true)
  })

  it('修改进行中返回的读取不能用', async () => {
    const { edits } = setup()
    const { request, settle } = requests()
    void edits.run(request('移出'), () => {})
    const usable = edits.read()
    expect(usable()).toBe(false)
    await settle(true)
    expect(usable()).toBe(false)
  })

  it('读取开始之后发起的修改，即使在读取返回前已经完成，读到的也不能用', async () => {
    const { edits } = setup()
    const { request, settle } = requests()
    const usable = edits.read()
    void edits.run(request('移动'), () => {})
    await settle(true)
    expect(usable()).toBe(false)
  })

  it('最后一个修改完成之后重新读取一次', async () => {
    const { edits, rereads } = setup()
    const { request, settle } = requests()
    void edits.run(request('移出'), () => {})
    void edits.run(request('移动'), () => {})
    await settle(true)
    expect(rereads()).toBe(0)
    await settle(true)
    expect(rereads()).toBe(1)
    expect(edits.read()()).toBe(true)
  })

  it('修改按发起的先后逐个请求，前一个完成之后才发下一个', async () => {
    const { edits } = setup()
    const { request, sent, settle } = requests()
    void edits.run(request('移到最前'), () => {})
    void edits.run(request('移到最后'), () => {})
    await flush()
    expect(sent).toEqual(['移到最前'])
    await settle(true)
    expect(sent).toEqual(['移到最前', '移到最后'])
  })

  it('一次失败之后，排在后面的不再发出，列表回到失败的那次之前，结束后重新读取一次', async () => {
    const { edits, rereads } = setup()
    const { request, sent, settle } = requests()
    const rolledBack: string[] = []
    let shown = '第一次移动之后'
    void edits.run(request('第一次移动'), () => {
      rolledBack.push('第一次移动')
      shown = '移动之前'
    })
    shown = '第二次移动之后'
    const done = edits.run(request('第二次移动'), () => {
      rolledBack.push('第二次移动')
      shown = '第一次移动之后'
    })
    await settle(false)
    await done
    expect(sent).toEqual(['第一次移动'])
    expect(rolledBack).toEqual(['第一次移动'])
    expect(shown).toBe('移动之前')
    expect(rereads()).toBe(1)
  })

  it('失败之后新发起的修改照常请求', async () => {
    const { edits } = setup()
    const { request, sent, settle } = requests()
    void edits.run(request('移动'), () => {})
    await settle(false)
    void edits.run(request('移出'), () => {})
    await flush()
    expect(sent).toEqual(['移动', '移出'])
  })

  it('移出失败时回到移出之前，全部完成之后重新读取', async () => {
    const { edits, rereads } = setup()
    const { request, settle } = requests()
    let items = ['晴天', '七里香']
    const before = items
    items = ['七里香']
    const done = edits.run(request('移出'), () => {
      items = before
    })
    await settle(false)
    await done
    expect(items).toEqual(['晴天', '七里香'])
    expect(rereads()).toBe(1)
  })
})
