import { describe, expect, it } from 'vitest'
import { linkOf, pickableLink } from './link'

describe('linkOf', () => {
  it.each([
    ['  http://example.com/a.mp3 ', 'http://example.com/a.mp3'],
    ['HTTPS://Example.com/A', 'HTTPS://Example.com/A'],
  ])('%s 是链接', (input, link) => {
    expect(linkOf(input)).toBe(link)
  })

  it.each(['晴天'])('%s 不是链接', (input) => {
    expect(linkOf(input)).toBeNull()
  })

  it.each([
    'https://user@example.com/a',
  ])('服务端不接受的 %s 不是链接', (input) => {
    expect(linkOf(input)).toBeNull()
  })
})

describe('pickableLink', () => {
  it.each([
    'https://www.bilibili.com/video/BV1xx411c7mD?p=2',
    'https://m.bilibili.com/video/BV1xx411c7mD',
    'https://bilibili.com/video/av170001',
    'HTTPS://B23.TV/qingtian',
  ])('临时房间可以点 B 站的链接 %s', (link) => {
    expect(pickableLink(` ${link} `, 'ephemeral')).toBe(link)
  })

  it.each([
    'https://www.youtube.com/watch?v=x',
    'https://notbilibili.com/video/BV1xx411c7mD',
    'https://bilibili.com.example/video/BV1xx411c7mD',
  ])('其他链接 %s 只在常驻房间可以点', (link) => {
    expect(pickableLink(link, 'ephemeral')).toBeNull()
    expect(pickableLink(link, 'persistent')).toBe(link)
  })

  it('不在房间里时都不能点', () => {
    expect(pickableLink('https://www.bilibili.com/video/BV1xx411c7mD', undefined)).toBeNull()
    expect(pickableLink('https://www.youtube.com/watch?v=x', undefined)).toBeNull()
  })

  it('不是链接时都不能点', () => {
    expect(pickableLink('晴天', 'persistent')).toBeNull()
    expect(pickableLink('晴天', 'ephemeral')).toBeNull()
  })
})
