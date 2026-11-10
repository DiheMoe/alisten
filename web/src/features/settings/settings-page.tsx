import type { SectionItem } from '@/shared/ui'
import { Contrast, Gauge, Music, Shield, UserRound } from 'lucide-react'
import { useEffect } from 'react'
import { volumeStore } from '@/features/playback'
import { useStore } from '@/shared/lib/use-store'
import { SectionLayout } from '@/shared/ui'
import { appearanceStore } from './appearance-store'
import { performanceStore } from './performance-store'
import { profileStore } from './profile-store'
import { AccountSettings, IdentityEntry } from './settings-account'
import { AppearanceSettings } from './settings-appearance'
import { sectionSummaries } from './settings-labels'
import { PerformanceSettings } from './settings-performance'
import { PlaybackSettings } from './settings-playback'
import { PrivacySettings } from './settings-privacy'
import { useSite } from './use-site'
import './settings.css'

export type SettingsSection = 'account' | 'appearance' | 'playback' | 'performance' | 'privacy'

// section 为 null 时地址是 /settings：宽屏显示账号，窄屏显示分类列表。onSection(null) 回到分类列表。
export function SettingsPage({ section, onSection, onOpenHistory }: {
  section: SettingsSection | null
  onSection: (section: SettingsSection | null) => void
  onOpenHistory: () => void
}) {
  const { site, reload: reloadSite } = useSite()
  const summaries = sectionSummaries({
    theme: useStore(appearanceStore, s => s.theme),
    level: useStore(performanceStore, s => s.level),
    volume: useStore(volumeStore, s => s.volume),
    muted: useStore(volumeStore, s => s.muted),
    recordListens: useStore(profileStore, s => s.recordListens),
  })
  // 资料的读取失败由各分类就地显示。
  useEffect(() => {
    void profileStore.load().catch(() => {})
  }, [])
  const sections: SectionItem<SettingsSection>[] = [
    { id: 'account', label: '账号', icon: UserRound, hiddenInList: true },
    { id: 'appearance', label: '外观', icon: Contrast, summary: summaries.appearance },
    { id: 'playback', label: '播放与提醒', icon: Music, summary: summaries.playback },
    { id: 'performance', label: '性能', icon: Gauge, summary: summaries.performance },
    { id: 'privacy', label: '隐私与记录', icon: Shield, summary: summaries.privacy },
  ]
  const shown = section ?? 'account'
  return (
    <SectionLayout
      title="设置"
      sections={sections}
      current={section}
      defaultSection="account"
      onSelect={onSection}
      header={<IdentityEntry onOpen={() => onSection('account')} />}
    >
      {shown === 'account' && <AccountSettings site={site} onRetrySite={reloadSite} />}
      {shown === 'appearance' && <AppearanceSettings />}
      {shown === 'playback' && <PlaybackSettings />}
      {shown === 'performance' && <PerformanceSettings />}
      {shown === 'privacy' && <PrivacySettings onOpenHistory={onOpenHistory} />}
    </SectionLayout>
  )
}
