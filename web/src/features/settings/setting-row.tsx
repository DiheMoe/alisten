import type { ReactNode } from 'react'
import { useSettingSave } from '@/shared/lib/use-setting-save'
import { SaveStatus, Switch } from '@/shared/ui'

// id 是行内控件的 id。标题用 label 指向这个控件，说明和错误的 id 都由 id 派生。
export function SettingRow({ id, title, description, error, status, labelled = true, children }: {
  id: string
  title: ReactNode
  description?: ReactNode
  error?: string | null
  status?: ReactNode
  // 控件不能被 label 指向时（比如分段控件自带 aria-label）为 false。
  labelled?: boolean
  children: ReactNode
}) {
  return (
    <div className="section-row">
      <div className="section-row-copy">
        {labelled ? <label htmlFor={id} className="section-row-title">{title}</label> : <span className="section-row-title">{title}</span>}
        {description && <p id={`${id}-description`} className="section-row-description">{description}</p>}
        {error && <p id={`${id}-error`} className="settings-row-error text-ui-caption" role="alert">{error}</p>}
      </div>
      <div className="settings-row-control">
        {status}
        {children}
      </div>
    </div>
  )
}

function describedBy(id: string, error: string | null): string {
  return error ? `${id}-description ${id}-error` : `${id}-description`
}

// 放在 SettingRow 里的开关，说明和错误由同一个 id 派生。
export function SettingSwitch({ id, checked, error, disabled, onChange }: { id: string, checked: boolean, error: string | null, disabled?: boolean, onChange: (checked: boolean) => void }) {
  return <Switch id={id} checked={checked} onChange={onChange} disabled={disabled} aria-describedby={describedBy(id, error)} />
}

export function SwitchRow({ id, title, description, checked, onSave }: {
  id: string
  title: string
  description: ReactNode
  checked: boolean
  onSave: (checked: boolean) => void | Promise<void>
}) {
  const setting = useSettingSave(checked, onSave)
  return (
    <SettingRow id={id} title={title} description={description} error={setting.state.error} status={<SaveStatus status={setting.state.status} version={setting.savedVersion} />}>
      <SettingSwitch id={id} checked={setting.value} error={setting.state.error} onChange={setting.change} />
    </SettingRow>
  )
}
