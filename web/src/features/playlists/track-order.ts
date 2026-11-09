// 与后端的 playlist.Move 一致：order 是显示顺序，把 moved 按给出的先后放到 before 之前，没有 before 时放到最后。
// moved 里不在 order 中的忽略，重复的只算一次；都不在 order 中时原样返回，此时不检查 before 是否还在。
export function moveTracks<T>(order: readonly T[], moved: readonly T[], before?: T): T[] {
  if (before !== undefined && moved.includes(before))
    throw new Error('before 不能是要移动的歌')
  const present = new Set(order)
  const block = [...new Set(moved)].filter(item => present.has(item))
  if (block.length === 0)
    return [...order]
  const blocked = new Set(block)
  const rest = order.filter(item => !blocked.has(item))
  const at = before === undefined ? rest.length : rest.indexOf(before)
  if (at < 0)
    throw new Error('歌单已经变了，刷新之后再试')
  return [...rest.slice(0, at), ...block, ...rest.slice(at)]
}

// 一次移动请求：把 moved 按 order 中的先后放到 before 之前，结果是 order。
export interface MovePlan<T> {
  order: T[]
  moved: T[]
  before: T | undefined
}

// 把 moved 收拢成一段，放在其余的歌中第 at 首之前；顺序不变时返回 null，不必发请求。
function place<T>(order: readonly T[], moved: ReadonlySet<T>, at: number): MovePlan<T> | null {
  const block = order.filter(item => moved.has(item))
  const before = order.filter(item => !moved.has(item))[at]
  const next = moveTracks(order, block, before)
  if (next.every((item, index) => item === order[index]))
    return null
  return { order: next, moved: block, before }
}

// index 是插入位置，取 0 到 order.length，表示放在 order[index] 之前。before 取插入位置之后第一首不移动的歌。
export function dropAt<T>(order: readonly T[], moved: ReadonlySet<T>, index: number): MovePlan<T> | null {
  return place(order, moved, order.slice(0, index).filter(item => !moved.has(item)).length)
}

// 上移时整段放到第一首要移动的歌前面那首之前，下移时放到最后一首要移动的歌后面那首之后。
// 不相连的歌会先收拢到一起，与拖动多首时一致，所以一次请求就能完成。
export function moveByOne<T>(order: readonly T[], moved: ReadonlySet<T>, delta: -1 | 1): MovePlan<T> | null {
  const indexes = order.flatMap((item, index) => moved.has(item) ? [index] : [])
  if (indexes.length === 0)
    return null
  const edge = delta < 0 ? indexes[0] : indexes[indexes.length - 1]
  const restBefore = edge - indexes.filter(index => index < edge).length
  const restCount = order.length - indexes.length
  return place(order, moved, Math.min(restCount, Math.max(0, restBefore + delta)))
}
