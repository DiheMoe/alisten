import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyText } from './clipboard'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('copyText', () => {
  it('写进剪贴板之后返回 true', async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>(() => Promise.resolve())
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    await expect(copyText('https://alisten.test/r/party')).resolves.toBe(true)
    expect(writeText.mock.calls).toEqual([['https://alisten.test/r/party']])
  })

  it('用户拒绝授权时返回 false', async () => {
    vi.stubGlobal('navigator', { clipboard: { writeText: () => Promise.reject(new DOMException('拒绝', 'NotAllowedError')) } })
    await expect(copyText('x')).resolves.toBe(false)
  })

  it('非安全上下文没有剪贴板接口，返回 false', async () => {
    vi.stubGlobal('navigator', {})
    await expect(copyText('x')).resolves.toBe(false)
  })
})
