import type { CSSProperties, KeyboardEvent } from 'react'
import { useRef } from 'react'
import { rovingIndex } from '@/shared/lib/roving'

export function SegmentedControl<T extends string>({ value, options, onChange, label }: {
  value: T
  options: { value: T, label: string }[]
  onChange: (value: T) => void
  label: string
}) {
  const rootRef = useRef<HTMLDivElement>(null)
  const selected = options.findIndex(option => option.value === value)
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const next = rovingIndex(event.key, Math.max(selected, 0), options.length)
    if (next === null)
      return
    event.preventDefault()
    rootRef.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]')[next]?.focus()
    if (next !== selected)
      onChange(options[next].value)
  }
  return (
    <div
      ref={rootRef}
      role="radiogroup"
      aria-label={label}
      className="segmented-control"
      style={{ '--segment-count': options.length, '--segment-index': selected } as CSSProperties}
      onKeyDown={onKeyDown}
    >
      {selected >= 0 && <span className="segmented-control-thumb" aria-hidden="true" />}
      {options.map((option, index) => (
        <button
          type="button"
          role="radio"
          key={option.value}
          aria-checked={index === selected}
          tabIndex={index === Math.max(selected, 0) ? 0 : -1}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}
