import { afterEach, expect, it, vi } from 'vitest'

class FakeStorage {
  readonly #items = new Map<string, string>()

  get length(): number {
    return this.#items.size
  }

  key(index: number): string | null {
    return [...this.#items.keys()][index] ?? null
  }

  getItem(key: string): string | null {
    return this.#items.get(key) ?? null
  }

  setItem(key: string, value: string): void {
    this.#items.set(key, value)
  }

  removeItem(key: string): void {
    this.#items.delete(key)
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

it('换身份时删除所有房间的票据，其他数据不动', async () => {
  const local = new FakeStorage()
  vi.stubGlobal('localStorage', local)
  local.setItem('alisten.ticket.room-1', 't1')
  local.setItem('alisten.member-token', 'm')
  local.setItem('alisten.ticket.room-2', 't2')
  local.setItem('alisten.volume', '80')
  const { deleteAllTickets } = await import('./credentials')
  deleteAllTickets()
  expect(local.getItem('alisten.ticket.room-1')).toBeNull()
  expect(local.getItem('alisten.ticket.room-2')).toBeNull()
  expect(local.getItem('alisten.member-token')).toBe('m')
  expect(local.getItem('alisten.volume')).toBe('80')
})
