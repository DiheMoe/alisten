import { Store } from '@/shared/lib/store'

export type Theme = 'dark' | 'light' | 'system'

export const themes: Theme[] = ['dark', 'light', 'system']
export const fontSizes = [12, 14, 16, 18]

const defaultTheme: Theme = 'dark'
const defaultFontSize = 14
const themeKey = 'alisten.theme'
const fontSizeKey = 'alisten.font-size'

function loadTheme(): Theme {
  const stored = localStorage.getItem(themeKey)
  return themes.find(theme => theme === stored) ?? defaultTheme
}

function loadFontSize(): number {
  const stored = Number(localStorage.getItem(fontSizeKey))
  return fontSizes.includes(stored) ? stored : defaultFontSize
}

class AppearanceStore extends Store<{ theme: Theme, fontSize: number }> {
  constructor() {
    super({ theme: defaultTheme, fontSize: defaultFontSize })
  }

  start(): void {
    this.setState({ theme: loadTheme(), fontSize: loadFontSize() })
    this.#apply()
    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', this.#syncThemeColor)
  }

  update(theme: Theme, fontSize: number): void {
    localStorage.setItem(themeKey, theme)
    localStorage.setItem(fontSizeKey, String(fontSize))
    this.setState({ theme, fontSize })
    this.#apply()
  }

  #apply(): void {
    const root = document.documentElement
    root.dataset.theme = this.state.theme
    // 只改 --ui-font-size，不改根元素的字号：间距和布局尺寸不随字号缩放。
    root.style.setProperty('--ui-font-size', `${this.state.fontSize}px`)
    this.#syncThemeColor()
  }

  #syncThemeColor = (): void => {
    const frame = getComputedStyle(document.documentElement).getPropertyValue('--color-frame')
    document.querySelector('meta[name="theme-color"]')!.setAttribute('content', frame)
  }
}

export const appearanceStore = new AppearanceStore()
