import type { KeyboardEvent } from 'react'
import type { Theme } from './appearance-store'
import { Check } from 'lucide-react'
import { useRef } from 'react'
import { rovingIndex } from '@/shared/lib/roving'
import { useSettingSave } from '@/shared/lib/use-setting-save'
import { useStore } from '@/shared/lib/use-store'
import { SaveStatus, SegmentedControl } from '@/shared/ui'
import { appearanceStore, fontSizes, themes } from './appearance-store'
import { SettingRow } from './setting-row'
import { themeNames } from './settings-labels'

function ThemePicker() {
  const saved = useStore(appearanceStore, s => s.theme)
  const setting = useSettingSave(saved, (theme: Theme) => appearanceStore.update(theme, appearanceStore.state.fontSize))
  const groupRef = useRef<HTMLDivElement>(null)
  const selected = themes.indexOf(setting.value)
  // 方向键在选项之间移动并选中，和原生的单选按钮一样。
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const next = rovingIndex(event.key, selected, themes.length)
    if (next === null)
      return
    event.preventDefault()
    groupRef.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]')[next]?.focus()
    setting.change(themes[next])
  }
  return (
    <>
      <div className="section-group-label settings-group-label">
        <span id="settings-theme-label">主题</span>
        <SaveStatus status={setting.state.status} version={setting.savedVersion} />
      </div>
      <div ref={groupRef} role="radiogroup" aria-labelledby="settings-theme-label" aria-describedby={setting.state.error ? 'settings-theme-error' : undefined} className="settings-themes" onKeyDown={onKeyDown}>
        {themes.map((theme, index) => (
          <button
            type="button"
            role="radio"
            key={theme}
            className="settings-theme"
            data-theme-preview={theme}
            aria-checked={index === selected}
            tabIndex={index === selected ? 0 : -1}
            onClick={() => setting.change(theme)}
          >
            <span className="settings-theme-preview" aria-hidden="true">
              <i />
              <i />
            </span>
            <span className="settings-theme-caption">
              {themeNames[theme]}
              {index === selected && <Check size={14} strokeWidth={2.4} aria-hidden="true" />}
            </span>
          </button>
        ))}
      </div>
      {setting.state.error && <p id="settings-theme-error" className="settings-row-error text-ui-caption" role="alert">{setting.state.error}</p>}
    </>
  )
}

function FontSizeRow() {
  const saved = useStore(appearanceStore, s => String(s.fontSize))
  const setting = useSettingSave(saved, (size: string) => appearanceStore.update(appearanceStore.state.theme, Number(size)))
  return (
    <SettingRow
      id="settings-font-size"
      title="房间里正在播放《晴天》"
      description="预览会跟着字号变化，布局和间距不变"
      error={setting.state.error}
      status={<SaveStatus status={setting.state.status} version={setting.savedVersion} />}
      labelled={false}
    >
      <SegmentedControl
        label="界面字号"
        value={setting.value}
        options={fontSizes.map(size => ({ value: String(size), label: String(size) }))}
        onChange={setting.change}
      />
    </SettingRow>
  )
}

export function AppearanceSettings() {
  return (
    <>
      <h2 className="settings-title">外观</h2>
      <ThemePicker />
      <p className="section-group-label">界面字号</p>
      <div className="section-group">
        <FontSizeRow />
      </div>
    </>
  )
}
