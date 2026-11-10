// 可见的名字由 <label htmlFor={id}> 提供。按钮可以被 label 关联，点击 label 也会切换开关。
export function Switch({ checked, onChange, disabled = false, id, 'aria-describedby': describedBy }: {
  'checked': boolean
  'onChange': (checked: boolean) => void
  'disabled'?: boolean
  'id'?: string
  'aria-describedby'?: string
}) {
  return (
    <button
      type="button"
      role="switch"
      id={id}
      className="switch"
      aria-checked={checked}
      aria-describedby={describedBy}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    >
      <span className="switch-thumb" aria-hidden="true" />
    </button>
  )
}
