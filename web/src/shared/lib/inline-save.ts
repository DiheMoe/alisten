// saved 是已经保存的值，draft 是输入框里的值；两者不同就是“编辑中”。
export interface InlineSaveState {
  draft: string
  saved: string
  status: 'idle' | 'saving' | 'saved' | 'error'
  error: string | null
}

// succeeded 的 value 是提交的值；external 是外部传入的新值，比如其他设备改了资料。
export type InlineSaveEvent
  = | { type: 'edit', value: string }
    | { type: 'submit' }
    | { type: 'succeeded', value: string }
    | { type: 'failed', error: string }
    | { type: 'revert' }
    | { type: 'external', value: string }

export function inlineSave(state: InlineSaveState, event: InlineSaveEvent): InlineSaveState {
  switch (event.type) {
    case 'edit':
      return { ...state, draft: event.value }
    case 'submit':
      if (state.status === 'saving')
        return state
      if (state.draft === state.saved)
        return state.status === 'error' ? { ...state, status: 'idle', error: null } : state
      return { ...state, status: 'saving', error: null }
    case 'succeeded':
      return state.status === 'saving' ? { ...state, saved: event.value, status: 'saved', error: null } : state
    case 'failed':
      return state.status === 'saving' ? { ...state, status: 'error', error: event.error } : state
    case 'revert':
      // 已经发出的保存撤不回来，等它的结果。
      if (state.status === 'saving')
        return state
      return { ...state, draft: state.saved, status: 'idle', error: null }
    case 'external':
      if (event.value === state.saved)
        return state
      // 保存中或者编辑中时不覆盖输入框，免得冲掉用户正在输入的内容。
      if (state.status === 'saving' || state.draft !== state.saved)
        return { ...state, saved: event.value }
      return { ...state, draft: event.value, saved: event.value }
  }
}
