let frame: number | null = null
let lastHeight = -1

// 现代浏览器用 CSS 的 100dvh，--vh 只是旧浏览器的兜底。
// 地址栏收放会持续触发 resize，所以合并到下一帧，数值不变时不写入。
export function setViewportHeight(): void {
  if (frame !== null)
    return
  frame = requestAnimationFrame(() => {
    frame = null
    if (window.innerHeight === lastHeight)
      return
    lastHeight = window.innerHeight
    document.documentElement.style.setProperty('--vh', `${lastHeight * 0.01}px`)
  })
}
