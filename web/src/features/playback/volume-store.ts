import { Store } from '@/shared/lib/store'

const volumeKey = 'alisten.volume'
const mutedKey = 'alisten.muted'
const defaultVolume = 75

function loadVolume(): number {
  const stored = Number(localStorage.getItem(volumeKey) ?? defaultVolume)
  return Number.isFinite(stored) ? Math.min(100, Math.max(0, stored)) : defaultVolume
}

class VolumeStore extends Store<{ volume: number, muted: boolean }> {
  constructor() {
    super({ volume: defaultVolume, muted: false })
  }

  start(): void {
    this.setState({ volume: loadVolume(), muted: localStorage.getItem(mutedKey) === 'true' })
  }

  setVolume(volume: number): void {
    const clamped = Math.min(100, Math.max(0, volume))
    localStorage.setItem(volumeKey, String(clamped))
    this.setState({ volume: clamped })
  }

  setMuted(muted: boolean): void {
    localStorage.setItem(mutedKey, String(muted))
    this.setState({ muted })
  }
}

export const volumeStore = new VolumeStore()
