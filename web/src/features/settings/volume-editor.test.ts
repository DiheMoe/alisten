import { describe, expect, it } from 'vitest'
import { beginVolumeEdit, changeVolumeDraft, commitVolumeDraft, initialVolumeEditor, syncVolumeDraft } from './volume-editor'

describe('音量草稿', () => {
  it('非编辑时同步外部音量', () => {
    const editor = initialVolumeEditor(75)
    expect(syncVolumeDraft(editor, 40)).toEqual({ draft: '40', editing: false })
  })

  it('编辑期间不被外部音量覆盖', () => {
    const editor = changeVolumeDraft(beginVolumeEdit(initialVolumeEditor(75)), '20')
    expect(syncVolumeDraft(editor, 90)).toEqual({ draft: '20', editing: true })
  })

  it('提交空值或非法输入时恢复有效音量', () => {
    for (const draft of ['', '   ', 'abc']) {
      const editor = changeVolumeDraft(beginVolumeEdit(initialVolumeEditor(75)), draft)
      expect(commitVolumeDraft(editor, 75)).toEqual({ editor: { draft: '75', editing: false }, value: null })
    }
  })

  it('提交时取整并限制范围', () => {
    for (const testCase of [
      { draft: '-1', want: 0 },
      { draft: '0', want: 0 },
      { draft: '12.6', want: 13 },
      { draft: '100', want: 100 },
      { draft: '101', want: 100 },
    ]) {
      const editor = changeVolumeDraft(beginVolumeEdit(initialVolumeEditor(75)), testCase.draft)
      expect(commitVolumeDraft(editor, 75).value).toBe(testCase.want)
    }
  })

  it('提交后退出编辑状态并显示提交值', () => {
    const editor = changeVolumeDraft(beginVolumeEdit(initialVolumeEditor(75)), '42')
    expect(commitVolumeDraft(editor, 75)).toEqual({ editor: { draft: '42', editing: false }, value: 42 })
  })
})
