// 返回一组选项里方向键要去的那一项，到头之后从另一头接着走。不是这几个键时返回 null。
// arrows 为 'all' 时规则和原生的单选按钮一样，← 和 ↑ 等价，→ 和 ↓ 等价。arrows 为 'vertical' 时不认 ← 和 →。
export function rovingIndex(key: string, index: number, count: number, arrows: 'all' | 'vertical' = 'all'): number | null {
  if (!count || (arrows === 'vertical' && (key === 'ArrowLeft' || key === 'ArrowRight')))
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
