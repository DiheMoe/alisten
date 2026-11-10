// 页码条最多 7 项：首页、省略号、当前页和前后各一页、省略号、末页。不超过 7 页时全部列出。
// 省略号只盖住一页时，直接显示那一页，因为占的位置一样。
export function pageItems(current: number, totalPages: number): (number | 'gap')[] {
  if (totalPages <= 7)
    return Array.from({ length: totalPages }, (_, index) => index + 1)
  const pages = [...new Set([1, current - 1, current, current + 1, totalPages])]
    .filter(page => page >= 1 && page <= totalPages)
    .sort((a, b) => a - b)
  const items: (number | 'gap')[] = []
  let previous = 0
  for (const page of pages) {
    if (page - previous === 2)
      items.push(previous + 1)
    else if (page - previous > 2)
      items.push('gap')
    items.push(page)
    previous = page
  }
  return items
}
