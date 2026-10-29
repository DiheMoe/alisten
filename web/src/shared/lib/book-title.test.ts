import { describe, expect, it } from 'vitest'
import { bookTitle } from './book-title'

describe('歌名的书名号', () => {
  it('加上书名号', () => {
    expect(bookTitle('晴天')).toBe('《晴天》')
  })

  it('歌名里已经有书名号时不再加', () => {
    expect(bookTitle('日语填词《5:20AM》（完整版）')).toBe('日语填词《5:20AM》（完整版）')
  })
})
