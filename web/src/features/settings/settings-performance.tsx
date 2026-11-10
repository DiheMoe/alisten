import type { PerformanceLevel } from './performance-store'
import { useSettingSave } from '@/shared/lib/use-setting-save'
import { useStore } from '@/shared/lib/use-store'
import { SaveStatus, SegmentedControl } from '@/shared/ui'
import { performanceLevels, performanceStore } from './performance-store'
import { SettingRow, SwitchRow } from './setting-row'
import { levelNames } from './settings-labels'

function LevelRow() {
  const saved = useStore(performanceStore, s => s.level)
  const setting = useSettingSave(saved, (level: PerformanceLevel) => performanceStore.update(level, performanceStore.state.reducedMotion))
  const { name, description } = levelNames[setting.value]
  return (
    <SettingRow id="settings-performance" title="性能模式" description={`${name}：${description}`} error={setting.state.error} status={<SaveStatus status={setting.state.status} version={setting.savedVersion} />} labelled={false}>
      <SegmentedControl
        label="性能模式"
        value={setting.value}
        options={performanceLevels.map(level => ({ value: level, label: levelNames[level].name }))}
        onChange={setting.change}
      />
    </SettingRow>
  )
}

export function PerformanceSettings() {
  const reducedMotion = useStore(performanceStore, s => s.reducedMotion)
  return (
    <>
      <h2 className="settings-title">性能</h2>
      <div className="section-group">
        <LevelRow />
        <SwitchRow
          id="settings-reduced-motion"
          title="减少动态效果"
          description="关闭界面动画和歌词的平滑滚动"
          checked={reducedMotion}
          onSave={on => performanceStore.update(performanceStore.state.level, on)}
        />
      </div>
    </>
  )
}
