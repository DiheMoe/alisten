// viewport 设了 interactive-widget=resizes-content，手机上输入法弹出时页面高度会缩到输入法上方，
// 播放栏和底部导航就夹在输入框和输入法之间。所以输入法开着时在根元素上标记 data-keyboard="open"，样式据此在手机布局下隐藏它们。

// 地址栏收放只让高度变化几十像素，输入法会占掉三分之一以上，所以高度不到最大高度的 0.8 才算输入法开着。
const keyboardRatio = 0.8

// 只看焦点不够，因为安卓上用返回键收起输入法时，焦点还留在文本框里。只看高度也不够，因为地址栏收放也会改变高度。
export class KeyboardDetector {
  #width = 0
  #fullHeight = 0

  // 最大高度按宽度分开记，宽度变了（比如旋转屏幕）就从当前高度重新记。
  observe(width: number, height: number, typing: boolean): boolean {
    if (width !== this.#width) {
      this.#width = width
      this.#fullHeight = height
    }
    this.#fullHeight = Math.max(this.#fullHeight, height)
    return typing && height < this.#fullHeight * keyboardRatio
  }
}

const nonTextInputs = new Set(['button', 'checkbox', 'color', 'file', 'hidden', 'image', 'radio', 'range', 'reset', 'submit'])

function isTextEntry(element: Element | null): boolean {
  if (element instanceof HTMLTextAreaElement)
    return true
  return element instanceof HTMLInputElement && !element.readOnly && !nonTextInputs.has(element.type)
}

export function watchKeyboard(): void {
  const detector = new KeyboardDetector()
  const root = document.documentElement
  let frame: number | null = null
  // focusout 时焦点还没落到下一个元素上，resize 也会连续触发，所以合并到下一帧再判断。
  const check = () => {
    if (frame !== null)
      return
    frame = requestAnimationFrame(() => {
      frame = null
      if (detector.observe(window.innerWidth, window.innerHeight, isTextEntry(document.activeElement)))
        root.dataset.keyboard = 'open'
      else
        delete root.dataset.keyboard
    })
  }
  check()
  window.addEventListener('resize', check, { passive: true })
  document.addEventListener('focusin', check)
  document.addEventListener('focusout', check)
}
