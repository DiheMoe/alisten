import { Check } from 'lucide-react'
import { useId } from 'react'
import { isImeKey } from '@/shared/lib/ime'
import { useInlineSave } from '@/shared/lib/use-inline-save'

export function InlineTextField({ label, value, onSave, placeholder, maxLength, type = 'text' }: {
  label: string
  value: string
  onSave: (value: string) => Promise<void>
  placeholder?: string
  maxLength?: number
  type?: 'text' | 'email'
}) {
  const errorId = useId()
  const { state, savedNotice, edit, submit, revert } = useInlineSave(value, onSave)
  const noticeVisible = state.status === 'saving' || savedNotice
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
          onChange={event => edit(event.target.value)}
          onBlur={() => void submit()}
          onKeyDown={(event) => {
            if (isImeKey(event.nativeEvent))
              return
            if (event.key === 'Enter') {
              event.preventDefault()
              void submit()
            }
            // 没有可恢复的内容时不拦截 Esc，外层的对话框照常关闭。
            else if (event.key === 'Escape' && (state.draft !== state.saved || state.error)) {
              event.preventDefault()
              revert()
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
