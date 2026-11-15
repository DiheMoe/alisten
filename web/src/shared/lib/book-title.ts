// 歌名里已经有书名号时不再加一层，因为 B 站的标题常带《》。
// 后端的 internal/adapter/in/api/convert.go 里也有一份 bookTitle，用在房间动态的文案里。改这条规则时两边要一起改，否则同一首歌在房间动态里和在前端的文案里写法会不一致。
export function bookTitle(title: string): string {
  return /[《》]/.test(title) ? title : `《${title}》`
}
