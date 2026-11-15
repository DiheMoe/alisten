import { Store } from '@/shared/lib/store'

export type NotificationTone = 'success' | 'error' | 'info'

export interface Notification {
  id: number
  tone: NotificationTone
  message: string
}

let nextId = 0

class NotificationStore extends Store<{ notifications: Notification[] }> {
  constructor() {
    super({ notifications: [] })
  }

  success(message: string, durationMs = 3000): void {
    this.#show('success', message, durationMs)
  }

  error(message: string, durationMs = 3000): void {
    this.#show('error', message, durationMs)
  }

  info(message: string, durationMs = 3000): void {
    this.#show('info', message, durationMs)
  }

  dismiss(id: number): void {
    this.setState({ notifications: this.state.notifications.filter(n => n.id !== id) })
  }

  #show(tone: NotificationTone, message: string, durationMs: number): void {
    const id = ++nextId
    this.setState({ notifications: [...this.state.notifications, { id, tone, message }] })
    setTimeout(() => this.dismiss(id), durationMs)
  }
}

export const notificationStore = new NotificationStore()
