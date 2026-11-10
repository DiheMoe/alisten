import { ChevronRight } from 'lucide-react'
import { historyStore, searchLimits } from '@/features/history'
import { useSettingSave } from '@/shared/lib/use-setting-save'
import { useStore } from '@/shared/lib/use-store'
import { SaveStatus, SegmentedControl } from '@/shared/ui'
import { profileStore } from './profile-store'
import { SettingRow, SwitchRow } from './setting-row'

function RecordListensRow() {
  const recordListens = useStore(profileStore, s => s.recordListens)
  const failed = useStore(profileStore, s => s.failed)
  const account = useStore(profileStore, s => s.account)
  const id = 'settings-record-listens'
  if (recordListens === null) {
    return (
      <SettingRow id={id} title="记录播放历史" description={failed ? '没有读到这项设置' : '正在读取…'} labelled={false}>
        {failed
          ? <button type="button" className="button-secondary settings-button" onClick={() => void profileStore.load().catch(() => {})}>重试</button>
          : <span className="settings-switch-placeholder" aria-hidden="true" />}
      </SettingRow>
    )
  }
  return (
    <SwitchRow
      id={id}
      title="记录播放历史"
      description={account ? '保存在服务器上，登录这个账号的设备共用' : '保存在服务器上'}
      checked={recordListens}
      onSave={on => profileStore.setRecordListens(on)}
    />
  )
}

function SearchLimitRow() {
  const saved = useStore(historyStore, s => String(s.searchLimit))
  const setting = useSettingSave(saved, (limit: string) => historyStore.updateSettings({ searchesEnabled: historyStore.state.searchesEnabled, searchLimit: Number(limit) }))
  return (
    <SettingRow id="settings-search-limit" title="搜索记录数量" error={setting.state.error} status={<SaveStatus status={setting.state.status} version={setting.savedVersion} />} labelled={false}>
      <SegmentedControl
        label="搜索记录数量"
        value={setting.value}
        options={searchLimits.map(limit => ({ value: String(limit), label: String(limit) }))}
        onChange={setting.change}
      />
    </SettingRow>
  )
}

export function PrivacySettings({ onOpenHistory }: { onOpenHistory: () => void }) {
  const searchesEnabled = useStore(historyStore, s => s.searchesEnabled)
  return (
    <>
      <h2 className="settings-title">隐私与记录</h2>
      <div className="section-group">
        <RecordListensRow />
        <button type="button" className="section-row" onClick={onOpenHistory}>
          <span className="section-row-copy">
            <span className="section-row-title">查看播放历史</span>
          </span>
          <ChevronRight className="settings-chevron" size={18} aria-hidden="true" />
        </button>
        <SwitchRow
          id="settings-search-history"
          title="保存搜索记录"
          description="只保存在这台设备上"
          checked={searchesEnabled}
          onSave={on => historyStore.updateSettings({ searchesEnabled: on, searchLimit: historyStore.state.searchLimit })}
        />
        {searchesEnabled && <SearchLimitRow />}
      </div>
    </>
  )
}
