// 改完即生效的控件（开关、分段选择）的保存状态。pending 是正在保存的新值，控件先显示这个值。保存失败时丢掉 pending，控件回到已保存的值。
export interface SettingSave<T> {
  pending: { value: T } | null
  status: 'idle' | 'saving' | 'saved' | 'failed'
  error: string | null
}

// expired：“已保存”的提示到时消失。
export type SettingSaveEvent<T>
  = | { type: 'start', value: T }
    | { type: 'succeeded' }
    | { type: 'failed', error: string }
    | { type: 'expired' }

export const idleSave: SettingSave<never> = { pending: null, status: 'idle', error: null }

export function settingSave<T>(state: SettingSave<T>, event: SettingSaveEvent<T>): SettingSave<T> {
  switch (event.type) {
    case 'start':
      // 一次只保存一个值，因为保存中再改的话，结果回来时分不清是哪一次的。
      return state.status === 'saving' ? state : { pending: { value: event.value }, status: 'saving', error: null }
    case 'succeeded':
      return state.status === 'saving' ? { pending: null, status: 'saved', error: null } : state
    case 'failed':
      return state.status === 'saving' ? { pending: null, status: 'failed', error: event.error } : state
    case 'expired':
      return state.status === 'saved' ? idleSave : state
  }
}

export function shownValue<T>(state: SettingSave<T>, saved: T): T {
  return state.pending ? state.pending.value : saved
}
