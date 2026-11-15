import { describe, expect, it } from 'vitest'
import { pickButton } from './pick-button'

const link = { source: 'link', id: 'https://example.com/song.mp3' } as const
const video = { source: 'bilibili', id: 'BV1xx411c7mD' } as const

describe('pickButton', () => {
  it('连上房间时可以点歌', () => {
    expect(pickButton(video, '晴天', { connected: true, ephemeral: true })).toEqual({ label: '点歌《晴天》', disabled: false })
    expect(pickButton(link, '晴天', { connected: true, ephemeral: false })).toEqual({ label: '点歌《晴天》', disabled: false })
  })

  it('没有连上房间时禁用', () => {
    expect(pickButton(video, '晴天', { connected: false, ephemeral: false })).toEqual({ label: '点歌《晴天》', disabled: true })
  })

  it('临时房间里链接的歌不能点，并说明原因', () => {
    expect(pickButton(link, '晴天', { connected: true, ephemeral: true })).toEqual({ label: '临时房间不能点链接里的歌', disabled: true })
  })
})
