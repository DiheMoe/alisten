// 删掉列表里的若干行之后焦点落到哪一行：真正删掉的第一行原来的位置上，现在排着的那一行（后面的行补上来，翻页补进来的也算）。
// before 和 after 是删除前后这一页各行的 key。那个位置上已经没有行时返回 null。
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
