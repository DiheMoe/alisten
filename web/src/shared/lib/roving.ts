// 返回单选组里方向键要去的那一项，规则和原生的单选按钮一样。← 和 ↑ 等价，→ 和 ↓ 等价，到头之后从另一头接着走。不是这几个键时返回 null。
export function rovingIndex(key: string, index: number, count: number): number | null {
  if (!count)
    return null
  switch (key) {
    case 'ArrowLeft':
    case 'ArrowUp':
      return (index - 1 + count) % count
    case 'ArrowRight':
    case 'ArrowDown':
      return (index + 1) % count
    case 'Home':
      return 0
    case 'End':
      return count - 1
    default:
      return null
  }
}
