export interface VolumeEditor {
  draft: string
  editing: boolean
}

export function initialVolumeEditor(volume: number): VolumeEditor {
  return { draft: String(volume), editing: false }
}

export function beginVolumeEdit(editor: VolumeEditor): VolumeEditor {
  return { ...editor, editing: true }
}

export function changeVolumeDraft(_editor: VolumeEditor, draft: string): VolumeEditor {
  return { draft, editing: true }
}

export function syncVolumeDraft(editor: VolumeEditor, volume: number): VolumeEditor {
  return editor.editing ? editor : { draft: String(volume), editing: false }
}

export function commitVolumeDraft(editor: VolumeEditor, fallbackVolume: number): { editor: VolumeEditor, value: number | null } {
  const parsed = Number(editor.draft)
  if (editor.draft.trim() === '' || !Number.isFinite(parsed)) {
    return { editor: { draft: String(fallbackVolume), editing: false }, value: null }
  }
  const value = Math.min(100, Math.max(0, Math.round(parsed)))
  return { editor: { draft: String(value), editing: false }, value }
}
