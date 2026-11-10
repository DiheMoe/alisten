// 删掉列表里的若干行之后，焦点落到真正删掉的第一行原来的位置上，也就是现在排在这个位置的那一行。后面的行补上来、翻页补进来的行都算。
// before 和 after 是删除前后这一页各行的 key。一行也没有真正删掉，或者这个位置上已经没有行时，返回 null。
export function rowAfterRemoval(before: readonly string[], after: readonly string[], removed: readonly string[]): string | null {
  const remaining = new Set(after)
  const requested = new Set(removed)
  const first = before.findIndex(key => requested.has(key) && !remaining.has(key))
  if (first === -1)
    return null
  let index = 0
  for (let i = first - 1; i >= 0; i--) {
    if (remaining.has(before[i]!)) {
      index = after.indexOf(before[i]!) + 1
      break
    }
  }
  return after[index] ?? null
}
