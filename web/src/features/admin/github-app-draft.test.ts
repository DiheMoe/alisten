import { describe, expect, it } from 'vitest'
import { githubAppDraft } from './github-app-draft'

describe('githubAppDraft', () => {
  it('去掉从 GitHub 复制时带上的首尾空白', () => {
    expect(githubAppDraft(' Ov23liExample0000001\n', '\t0123456789abcdef ')).toEqual({ clientId: 'Ov23liExample0000001', clientSecret: '0123456789abcdef' })
  })

  it('client ID 和 client secret 缺一个都不能保存', () => {
    expect(githubAppDraft('Ov23liExample0000001', '')).toBeNull()
    expect(githubAppDraft('Ov23liExample0000001', '  ')).toBeNull()
    expect(githubAppDraft('', '0123456789abcdef')).toBeNull()
  })
})
