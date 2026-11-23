import { Store } from '@/shared/lib/store'

const translationKey = 'alisten.lyrics-translation'
const wordsKey = 'alisten.lyrics-words'

function loadSwitch(key: string): boolean {
  return localStorage.getItem(key) !== 'false'
}

class LyricsDisplayStore extends Store<{ translation: boolean, words: boolean }> {
  constructor() {
    super({ translation: true, words: true })
  }

  start(): void {
    this.setState({ translation: loadSwitch(translationKey), words: loadSwitch(wordsKey) })
  }

  update(translation: boolean, words: boolean): void {
    localStorage.setItem(translationKey, String(translation))
    localStorage.setItem(wordsKey, String(words))
    this.setState({ translation, words })
  }
}

export const lyricsDisplayStore = new LyricsDisplayStore()
