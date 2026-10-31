import { useState } from 'react'
import { beginVolumeEdit, changeVolumeDraft, commitVolumeDraft, initialVolumeEditor, syncVolumeDraft } from './volume-editor'

interface VolumeInputProps {
  volume: number
  onCommit: (volume: number) => void
  sliderValue?: number
  id?: string
  className?: string
  disabled?: boolean
}

export function VolumeInput({ volume, onCommit, sliderValue, id, className = '', disabled = false }: VolumeInputProps) {
  const [editor, setEditor] = useState(() => initialVolumeEditor(volume))
  const syncedEditor = syncVolumeDraft(editor, volume)
  const commit = () => {
    const result = commitVolumeDraft(syncedEditor, volume)
    setEditor(result.editor)
    if (result.value !== null)
      onCommit(result.value)
  }
  return (
    <div className={`volume-control ${className}`}>
      <input
        id={id}
        className="volume-slider"
        type="range"
        min="0"
        max="100"
        value={sliderValue ?? volume}
        aria-label="音量"
        disabled={disabled}
        onChange={(event) => {
          const next = Number(event.target.value)
          setEditor(current => changeVolumeDraft(current, String(next)))
          onCommit(next)
        }}
      />
      <div className="volume-input-control">
        <input
          className="volume-input"
          type="number"
          min="0"
          max="100"
          step="1"
          inputMode="numeric"
          value={syncedEditor.draft}
          aria-label="音量百分比"
          disabled={disabled}
          onFocus={() => setEditor(current => beginVolumeEdit(current))}
          onChange={event => setEditor(current => changeVolumeDraft(current, event.target.value))}
          onBlur={commit}
          onKeyDown={(event) => {
            if (event.key !== 'Enter')
              return
            event.preventDefault()
            event.currentTarget.blur()
          }}
        />
        <span className="volume-unit" aria-hidden="true">%</span>
      </div>
    </div>
  )
}
