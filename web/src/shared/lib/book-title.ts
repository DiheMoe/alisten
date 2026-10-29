// 歌名里已经有书名号时不再加一层：B 站的标题常带《》。
export function bookTitle(title: string): string {
  return /[《》]/.test(title) ? title : `《${title}》`
}
