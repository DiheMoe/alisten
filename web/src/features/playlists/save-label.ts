import { bookTitle } from '@/shared/lib/book-title'

export function saveToPlaylistLabel(title: string): string {
  return `把${bookTitle(title)}保存到歌单`
}
