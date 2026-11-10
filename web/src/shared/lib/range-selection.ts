// 锚点是上一次点击的 id。按住 Shift 点击时选中锚点到这一项之间的全部，不取消范围外已经选中的项。
// 没有锚点，或者锚点已经不在列表里（比如翻了页）时，和普通点击一样只切换这一项。
export function toggleSelection(selected: ReadonlySet<string>, ids: readonly string[], id: string, anchor: string | null, shift: boolean): { selected: Set<string>, anchor: string } {
  const next = new Set(selected)
  const from = anchor === null ? -1 : ids.indexOf(anchor)
  const to = ids.indexOf(id)
  if (shift && from !== -1 && to !== -1) {
    for (const item of ids.slice(Math.min(from, to), Math.max(from, to) + 1))
      next.add(item)
  }
  else if (next.has(id)) {
    next.delete(id)
  }
  else {
    next.add(id)
  }
  return { selected: next, anchor: id }
}
