import { Store } from '@/shared/lib/store'

export type PerformanceLevel = 'high' | 'medium' | 'low' | 'off'

export const performanceLevels: PerformanceLevel[] = ['high', 'medium', 'low', 'off']

const defaultLevel: PerformanceLevel = 'medium'
const levelKey = 'alisten.performance'
const reducedMotionKey = 'alisten.reduced-motion'

function loadLevel(): PerformanceLevel {
  const stored = localStorage.getItem(levelKey)
  return performanceLevels.find(level => level === stored) ?? defaultLevel
}

class PerformanceStore extends Store<{ level: PerformanceLevel, reducedMotion: boolean }> {
  #systemReducedMotion: MediaQueryList | undefined

  constructor() {
    super({ level: defaultLevel, reducedMotion: false })
  }

  start(): void {
    this.setState({ level: loadLevel(), reducedMotion: localStorage.getItem(reducedMotionKey) === 'true' })
    this.#apply()
  }

  update(level: PerformanceLevel, reducedMotion: boolean): void {
    localStorage.setItem(levelKey, level)
    localStorage.setItem(reducedMotionKey, String(reducedMotion))
    this.setState({ level, reducedMotion })
    this.#apply()
  }

  smoothMotion(): boolean {
    const { level, reducedMotion } = this.state
    this.#systemReducedMotion ??= window.matchMedia('(prefers-reduced-motion: reduce)')
    return (level === 'high' || level === 'medium') && !reducedMotion && !this.#systemReducedMotion.matches
  }

  #apply(): void {
    const root = document.documentElement
    root.dataset.performance = this.state.level
    root.toggleAttribute('data-reduced-motion', this.state.reducedMotion)
  }
}

export const performanceStore = new PerformanceStore()
