import type { KeyboardEvent } from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'

export interface SelectOption {
  value: string
  label: string
}

interface SelectProps {
  id?: string
  value: string
  options: readonly SelectOption[]
  onChange: (value: string) => void
  ariaLabel?: string
  ariaDescribedBy?: string
  disabled?: boolean
  className?: string
}

export function Select({ id, value, options, onChange, ariaLabel, ariaDescribedBy, disabled = false, className = '' }: SelectProps) {
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(0)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const listboxRef = useRef<HTMLDivElement>(null)
  const generatedId = useId()
  const listboxId = `${id ?? generatedId}-options`
  const selectedIndex = Math.max(0, options.findIndex(option => option.value === value))
  const selectedOption = options[selectedIndex]

  useEffect(() => {
    if (open)
      listboxRef.current?.focus({ preventScroll: true })
  }, [open])

  useEffect(() => {
    if (!open)
      return
    const onPointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node))
        setOpen(false)
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [open])

  const close = () => {
    setOpen(false)
    triggerRef.current?.focus()
  }

  const choose = (index: number) => {
    const option = options[index]
    if (!option)
      return
    onChange(option.value)
    close()
  }

  const move = (direction: 1 | -1) => {
    if (!options.length)
      return
    setActiveIndex(index => (index + direction + options.length) % options.length)
  }

  const openMenu = (index = selectedIndex) => {
    setActiveIndex(index)
    setOpen(true)
  }

  const onTriggerKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (disabled)
      return
    if (event.key === 'ArrowDown' || event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      openMenu()
    }
    else if (event.key === 'ArrowUp') {
      event.preventDefault()
      openMenu(Math.max(0, options.length - 1))
    }
  }

  const onListboxKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        move(1)
        break
      case 'ArrowUp':
        event.preventDefault()
        move(-1)
        break
      case 'Home':
        event.preventDefault()
        setActiveIndex(0)
        break
      case 'End':
        event.preventDefault()
        setActiveIndex(Math.max(0, options.length - 1))
        break
      case 'Enter':
      case ' ':
        event.preventDefault()
        choose(activeIndex)
        break
      case 'Escape':
        event.preventDefault()
        close()
        break
      case 'Tab':
        setOpen(false)
        break
    }
  }

  return (
    <div ref={rootRef} className={`select${className ? ` ${className}` : ''}`}>
      <button
        ref={triggerRef}
        id={id}
        type="button"
        className="select-trigger"
        aria-label={ariaLabel}
        aria-describedby={ariaDescribedBy}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listboxId : undefined}
        disabled={disabled}
        onClick={() => open ? close() : openMenu()}
        onKeyDown={onTriggerKeyDown}
      >
        <span className="select-value">{selectedOption?.label ?? value}</span>
        <ChevronDown size={16} aria-hidden="true" />
      </button>
      {open && (
        <div
          ref={listboxRef}
          id={listboxId}
          className="select-menu"
          role="listbox"
          tabIndex={-1}
          aria-label={ariaLabel}
          aria-activedescendant={`${listboxId}-option-${activeIndex}`}
          onKeyDown={onListboxKeyDown}
        >
          {options.map((option, index) => (
            <button
              type="button"
              id={`${listboxId}-option-${index}`}
              className="select-option"
              role="option"
              aria-selected={option.value === value}
              data-active={index === activeIndex}
              key={option.value}
              onMouseEnter={() => setActiveIndex(index)}
              onClick={() => choose(index)}
            >
              <span>{option.label}</span>
              {option.value === value && <Check size={16} aria-hidden="true" />}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
