import type { InlineSaveEvent, InlineSaveState } from '@/shared/lib/inline-save'
import { Check } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { inlineSave } from '@/shared/lib/inline-save'

const savedNoticeMs = 1500

// onSave 失败时抛出 Error，它的 message 就地显示在输入框下面。
export function InlineTextField({ label, value, onSave, placeholder, maxLength, type = 'text' }: {
  label: string
  value: string
  onSave: (value: string) => Promise<void>
  placeholder?: string
  maxLength?: number
  type?: 'text' | 'email'
}) {
  const errorId = useId()
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

  const submit = async () => {
    if (savingRef.current || inlineSave(state, { type: 'submit' }).status !== 'saving') {
      apply({ type: 'submit' })
      return
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
    }
    catch (error) {
      apply({ type: 'failed', error: error instanceof Error && error.message ? error.message : '没有保存，再试一次。' })
    }
    finally {
      savingRef.current = false
    }
  }

  const noticeVisible = state.status === 'saving' || (state.status === 'saved' && savedNotice > 0 && state.draft === state.saved)
  return (
    <div className="inline-text-field">
      <div className="inline-text-field-box">
        <input
          type={type}
          value={state.draft}
          aria-label={label}
          placeholder={placeholder}
          maxLength={maxLength}
          aria-invalid={state.status === 'error'}
          aria-describedby={state.error ? errorId : undefined}
          aria-busy={state.status === 'saving'}
          onChange={event => apply({ type: 'edit', value: event.target.value })}
          onBlur={() => void submit()}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              void submit()
            }
            // 没有可恢复的内容时不拦截 Esc，外层的对话框照常关闭。
            else if (event.key === 'Escape' && (state.draft !== state.saved || state.error)) {
              event.preventDefault()
              apply({ type: 'revert' })
            }
          }}
        />
        <span className="inline-text-field-status text-ui-caption" role="status" data-visible={noticeVisible || undefined} data-saving={state.status === 'saving' || undefined}>
          {state.status === 'saving' && '保存中…'}
          {state.status === 'saved' && (
            <>
              <Check size={14} strokeWidth={2.2} aria-hidden="true" />
              已保存
            </>
          )}
        </span>
      </div>
      {state.error && <p id={errorId} className="inline-text-field-error text-ui-caption" role="alert">{state.error}</p>}
    </div>
  )
}
