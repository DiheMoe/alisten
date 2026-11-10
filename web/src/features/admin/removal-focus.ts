// 行是带 data-key 的 li，焦点落到这一行的第一个控件上。没有这一行时落到分类标题。
export function rowOrHeading(headingId: string, key: string | null): HTMLElement | null {
  const heading = document.getElementById(headingId)
  const row = key === null ? null : heading?.closest('section')?.querySelector<HTMLElement>(`li[data-key="${CSS.escape(key)}"] :is(input, button)`)
  return row ?? heading
}
