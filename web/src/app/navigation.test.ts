import { afterEach, describe, expect, it, vi } from 'vitest'

afterEach(() => {
  vi.doUnmock('@/features/rooms')
  vi.unstubAllGlobals()
})

describe('取消进入房间', () => {
  it('直接打开的房间链接换成发现房间，并且放弃进入', async () => {
    vi.resetModules()
    const entry = {
      state: { ref: 'my-room' as string | null, room: null },
      reset() {
        this.state = { ref: null, room: null }
      },
    }
    vi.doMock('@/features/rooms', () => ({ roomEntryStore: entry }))
    vi.stubGlobal('window', { history: { replaceState() {} } })
    const { navigationStore } = await import('./navigation')
    navigationStore.replace({ name: 'listen', ref: 'my-room', panel: null })

    navigationStore.leaveRoom()

    expect(navigationStore.state.route).toEqual({ name: 'lobby', filter: '', panel: null })
    expect(entry.state.ref).toBeNull()
  })
})
