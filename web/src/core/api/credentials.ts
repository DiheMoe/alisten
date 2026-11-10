export const memberTokenKey = 'alisten.member-token'
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

// 票据属于签发时的成员，换了身份之后都不能再用。倒序遍历，因为删除会让后面的键前移。
export function deleteAllTickets(): void {
  for (let i = localStorage.length - 1; i >= 0; i--) {
    const key = localStorage.key(i)
    if (key?.startsWith(ticketKeyPrefix))
      localStorage.removeItem(key)
  }
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
