import type { InlineSaveEvent, InlineSaveState } from './inline-save'
import { useEffect, useRef, useState } from 'react'
import { inlineSave } from './inline-save'

const savedNoticeMs = 1500

// 文本的保存流程：失焦或回车时 submit，Esc 时 revert。onSave 失败时抛出 Error，它的 message 是给用户看的原因。
// submit 返回提交之后输入框里的值是否已经保存：没有改动也算已经保存；正在保存或者保存失败时返回 false。
export function useInlineSave(value: string, onSave: (value: string) => Promise<void>) {
  const [state, setState] = useState<InlineSaveState>({ draft: value, saved: value, status: 'idle', error: null })
  const [seenValue, setSeenValue] = useState(value)
  const [savedNotice, setSavedNotice] = useState(0)
  // state 要到下一次渲染才更新，回车之后紧跟着失焦时，由这个 ref 阻止重复保存。
  const savingRef = useRef(false)
  const apply = (event: InlineSaveEvent) => setState(current => inlineSave(current, event))

  if (value !== seenValue) {
    setSeenValue(value)
    apply({ type: 'external', value })
  }

  useEffect(() => {
    if (!savedNotice)
      return
    const timer = setTimeout(setSavedNotice, savedNoticeMs, 0)
    return () => clearTimeout(timer)
  }, [savedNotice])

  const submit = async (): Promise<boolean> => {
    if (savingRef.current)
      return false
    if (inlineSave(state, { type: 'submit' }).status !== 'saving') {
      apply({ type: 'submit' })
      return true
    }
    savingRef.current = true
    const submitted = state.draft
    const savedBefore = state.saved
    apply({ type: 'submit' })
    try {
      await onSave(submitted)
      setState((current) => {
        const done = inlineSave(current, { type: 'succeeded', value: submitted })
        // 保存期间外部传入了新值时以它为准：服务端可能改写了提交的值，比如去掉首尾空白。
        return current.saved === savedBefore ? done : inlineSave(done, { type: 'external', value: current.saved })
      })
      setSavedNotice(count => count + 1)
      return true
    }
    catch (error) {
      apply({ type: 'failed', error: error instanceof Error && error.message ? error.message : '没有保存，再试一次。' })
      return false
    }
    finally {
      savingRef.current = false
    }
  }

  return {
    state,
    // “已保存”在保存成功后显示 1.5 秒；又改了输入框就不再显示。
    savedNotice: state.status === 'saved' && savedNotice > 0 && state.draft === state.saved,
    edit: (draft: string) => apply({ type: 'edit', value: draft }),
    submit,
    revert: () => apply({ type: 'revert' }),
  }
}
