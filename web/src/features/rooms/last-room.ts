const lastRoomKey = 'alisten.last-room'

export function loadLastRoom(): string | null {
  return localStorage.getItem(lastRoomKey)
}

export function saveLastRoom(roomId: string): void {
  localStorage.setItem(lastRoomKey, roomId)
}

export function clearLastRoom(): void {
  localStorage.removeItem(lastRoomKey)
}
