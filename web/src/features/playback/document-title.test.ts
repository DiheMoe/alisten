import { describe, expect, it } from 'vitest'
import { documentTitle } from './document-title'

describe('documentTitle', () => {
  it('没有在放的歌时只有品牌名', () => {
    expect(documentTitle('周末听歌会', null)).toBe('周末听歌会')
  })

  it('放歌时是歌名 - 歌手 | 品牌名', () => {
    expect(documentTitle('周末听歌会', { title: '晴天', artist: '周杰伦' })).toBe('晴天 - 周杰伦 | 周末听歌会')
  })

  it('歌手为空时不留多余的分隔符', () => {
    expect(documentTitle('Alisten', { title: '片头', artist: '' })).toBe('片头 | Alisten')
  })
})
