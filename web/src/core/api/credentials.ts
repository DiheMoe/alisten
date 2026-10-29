const memberTokenKey = 'alisten.member-token'
const adminTokenKey = 'alisten.admin-token'
const ticketKeyPrefix = 'alisten.ticket.'

export function loadMemberToken(): string | null {
  return localStorage.getItem(memberTokenKey)
}

export function saveMemberToken(token: string): void {
  localStorage.setItem(memberTokenKey, token)
}

export function deleteMemberToken(): void {
  localStorage.removeItem(memberTokenKey)
}

export function loadTicket(roomId: string): string | null {
  return localStorage.getItem(ticketKeyPrefix + roomId)
}

export function saveTicket(roomId: string, ticket: string): void {
  localStorage.setItem(ticketKeyPrefix + roomId, ticket)
}

export function deleteTicket(roomId: string): void {
  localStorage.removeItem(ticketKeyPrefix + roomId)
}

export function loadAdminToken(): string | null {
  return sessionStorage.getItem(adminTokenKey)
}

export function saveAdminToken(token: string): void {
  sessionStorage.setItem(adminTokenKey, token)
}

export function deleteAdminToken(): void {
  sessionStorage.removeItem(adminTokenKey)
}
