export interface Selection {
  keys: ReadonlySet<string>
  // Shift 点击从这里选起：最后一次逐首切换的那首。
  anchor: string | null
}

export const noSelection: Selection = { keys: new Set(), anchor: null }

export function toggle(selection: Selection, key: string): Selection {
  const keys = new Set(selection.keys)
  if (!keys.delete(key))
    keys.add(key)
  return { keys, anchor: key }
}

// 把锚点到 key 的一段加入选择；没有锚点，或者锚点已经不在列表里时，只切换 key。
export function selectRange(selection: Selection, order: readonly string[], key: string): Selection {
  const from = selection.anchor === null ? -1 : order.indexOf(selection.anchor)
  const to = order.indexOf(key)
  if (from < 0 || to < 0)
    return toggle(selection, key)
  const range = order.slice(Math.min(from, to), Math.max(from, to) + 1)
  return { keys: new Set([...selection.keys, ...range]), anchor: selection.anchor }
}

export function selectAll(order: readonly string[]): Selection {
  return { keys: new Set(order), anchor: null }
}

// 列表重新读取之后去掉已经不在里面的歌；没有变化时返回原对象。
export function retain(selection: Selection, order: readonly string[]): Selection {
  const present = new Set(order)
  const keys = [...selection.keys].filter(key => present.has(key))
  const anchor = selection.anchor !== null && present.has(selection.anchor) ? selection.anchor : null
  if (keys.length === selection.keys.size && anchor === selection.anchor)
    return selection
  return { keys: new Set(keys), anchor }
}
