import type { ReactNode } from 'react'
import type { Theme } from './appearance-store'
import type { PerformanceLevel } from './performance-store'
import { useEffect, useRef, useState } from 'react'
import { errorMessage } from '@/core/errors'
import { historyStore, searchLimits } from '@/features/history'
import { reminderStore, requestNotificationPermission, systemNotificationsSupported, volumeStore } from '@/features/playback'
import { useStore } from '@/shared/lib/use-store'
import { notificationStore } from '@/shared/ui'
import { appearanceStore, fontSizes, themes } from './appearance-store'
import { performanceLevels, performanceStore } from './performance-store'
import { profileStore } from './profile-store'
import './settings.css'

interface SettingsDraft {
  name: string
  email: string
  theme: Theme
  fontSize: number
  volume: number
  muted: boolean
  reminderEnabled: boolean
  systemNotifications: boolean
  level: PerformanceLevel
  reducedMotion: boolean
  searchesEnabled: boolean
  searchLimit: number
  playsEnabled: boolean
}

type DraftKey = keyof SettingsDraft

const themeNames: Record<Theme, string> = { dark: '深色', light: '浅色', system: '跟随系统' }

const levelNames: Record<PerformanceLevel, { name: string, description: string }> = {
  high: { name: '高质量', description: '所有动画效果，适合高性能设备。' },
  medium: { name: '平衡', description: '优化过的动画效果，推荐使用。' },
  low: { name: '省电', description: '简化动画效果，适合移动设备。' },
  off: { name: '极简', description: '关闭所有动画，对设备的负担最小。' },
}

function loadProfile(): void {
  void profileStore.load().catch((error: unknown) => {
    notificationStore.error(errorMessage(error))
  })
}

function SettingRow({ id, title, description, field = false, children }: {
  id: string
  title: string
  description: string
  field?: boolean
  children: ReactNode
}) {
  return (
    <div className={`settings-row${field ? ' settings-row-field' : ''}`}>
      <div className="settings-row-copy">
        <label htmlFor={id} className="settings-row-title text-ui-base">{title}</label>
        <p id={`${id}-description`} className="text-ui-caption muted">{description}</p>
      </div>
      <div className="settings-row-control">{children}</div>
    </div>
  )
}

export function SettingsPage() {
  const headingRef = useRef<HTMLHeadingElement>(null)
  const member = useStore(profileStore, s => s.member)
  const email = useStore(profileStore, s => s.email)
  const profileFailed = useStore(profileStore, s => s.failed)
  const theme = useStore(appearanceStore, s => s.theme)
  const fontSize = useStore(appearanceStore, s => s.fontSize)
  const volume = useStore(volumeStore, s => s.volume)
  const muted = useStore(volumeStore, s => s.muted)
  const reminderEnabled = useStore(reminderStore, s => s.enabled)
  const systemNotifications = useStore(reminderStore, s => s.systemNotifications)
  const level = useStore(performanceStore, s => s.level)
  const reducedMotion = useStore(performanceStore, s => s.reducedMotion)
  const searchesEnabled = useStore(historyStore, s => s.searchesEnabled)
  const searchLimit = useStore(historyStore, s => s.searchLimit)
  const playsEnabled = useStore(historyStore, s => s.playsEnabled)
  const [changes, setChanges] = useState<Partial<SettingsDraft>>({})
  const [saving, setSaving] = useState(false)
  // state 要到下一次渲染才更新，连续提交时由这个 ref 阻止重复保存。
  const savingRef = useRef(false)
  useEffect(() => {
    headingRef.current?.focus({ preventScroll: true })
    loadProfile()
  }, [])
  const current: SettingsDraft = { name: member?.name ?? '', email, theme, fontSize, volume, muted, reminderEnabled, systemNotifications, level, reducedMotion, searchesEnabled, searchLimit, playsEnabled }
  const draft: SettingsDraft = { ...current, ...changes }
  const changed = (keys: DraftKey[]) => keys.some(key => draft[key] !== current[key])
  const dirty = changed(Object.keys(changes) as DraftKey[])
  const update = <K extends DraftKey>(key: K, value: SettingsDraft[K]) => {
    setChanges(previous => ({ ...previous, [key]: value }))
  }
  const toggleSystemNotifications = (enabled: boolean) => {
    if (!enabled) {
      update('systemNotifications', false)
      return
    }
    void requestNotificationPermission().then((allowed) => {
      if (allowed)
        update('systemNotifications', true)
      else
        notificationStore.error('浏览器没有允许显示通知，可以在浏览器的网站设置里允许。')
    })
  }
  const save = async () => {
    if (savingRef.current || !dirty)
      return
    savingRef.current = true
    setSaving(true)
    const saved: DraftKey[] = []
    let failed = false
    try {
      if (changed(['name', 'email'])) {
        // 只发送改动过的字段：空邮箱会清除头像，本机没有保存邮箱时不能一并发送。
        const profile: { name?: string, email?: string } = {}
        if (changed(['name']))
          profile.name = draft.name.trim()
        if (changed(['email']))
          profile.email = draft.email.trim()
        if (await profileStore.save(profile))
          saved.push('name', 'email')
        else
          failed = true
      }
      if (changed(['theme', 'fontSize'])) {
        appearanceStore.update(draft.theme, draft.fontSize)
        saved.push('theme', 'fontSize')
      }
      if (changed(['volume', 'muted'])) {
        volumeStore.setVolume(draft.volume)
        volumeStore.setMuted(draft.muted)
        saved.push('volume', 'muted')
      }
      if (changed(['reminderEnabled', 'systemNotifications'])) {
        reminderStore.updateSettings({ enabled: draft.reminderEnabled, systemNotifications: draft.systemNotifications })
        saved.push('reminderEnabled', 'systemNotifications')
      }
      if (changed(['level', 'reducedMotion'])) {
        performanceStore.update(draft.level, draft.reducedMotion)
        saved.push('level', 'reducedMotion')
      }
      if (changed(['searchesEnabled', 'searchLimit', 'playsEnabled'])) {
        historyStore.updateSettings({ searchesEnabled: draft.searchesEnabled, searchLimit: draft.searchLimit, playsEnabled: draft.playsEnabled })
        saved.push('searchesEnabled', 'searchLimit', 'playsEnabled')
      }
      setChanges((previous) => {
        const rest = { ...previous }
        for (const key of saved)
          delete rest[key]
        return rest
      })
      if (!failed)
        notificationStore.success('已保存设置')
    }
    finally {
      savingRef.current = false
      setSaving(false)
    }
  }
  return (
    <section className="settings-page" aria-labelledby="settings-title">
      <header className="settings-page-header">
        <h1 id="settings-title" className="text-ui-xl" tabIndex={-1} ref={headingRef}>设置</h1>
      </header>
      <form
        className="settings-page-form"
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <div className="settings-page-body">
          <fieldset className="settings-fields" disabled={saving}>
            <div className="settings-content">
              <section className="settings-section" aria-labelledby="settings-profile-title">
                <h2 id="settings-profile-title" className="text-ui-lg">个人资料</h2>
                <div className="settings-group">
                  <SettingRow id="settings-name" title="昵称" description={member ? '房间里的其他人看到的名字。' : profileFailed ? '暂时无法获取你的资料。' : '正在获取你的资料…'} field>
                    {!member && profileFailed
                      ? <button type="button" className="button-secondary" aria-describedby="settings-name-description" onClick={loadProfile}>重试</button>
                      : <input id="settings-name" aria-describedby="settings-name-description" value={draft.name} onChange={event => update('name', event.target.value)} disabled={!member} required maxLength={80} autoComplete="nickname" />}
                  </SettingRow>
                  <SettingRow id="settings-email" title="邮箱" description="只用来显示 Gravatar 头像，服务器不会保存邮箱地址；留空就不显示头像。" field>
                    <input id="settings-email" aria-describedby="settings-email-description" type="email" value={draft.email} onChange={event => update('email', event.target.value)} disabled={!member} maxLength={320} autoComplete="email" placeholder="name@example.com" />
                  </SettingRow>
                </div>
              </section>
              <section className="settings-section" aria-labelledby="settings-appearance-title">
                <h2 id="settings-appearance-title" className="text-ui-lg">外观</h2>
                <div className="settings-group">
                  <SettingRow id="settings-theme" title="主题" description="选择界面的明暗外观。">
                    <select id="settings-theme" aria-describedby="settings-theme-description" value={draft.theme} onChange={event => update('theme', themes.find(value => value === event.target.value) ?? draft.theme)}>
                      {themes.map(value => <option key={value} value={value}>{themeNames[value]}</option>)}
                    </select>
                  </SettingRow>
                  <SettingRow id="settings-font-size" title="界面字号" description="调整文字大小，布局和间距保持不变。">
                    <select id="settings-font-size" aria-describedby="settings-font-size-description" value={draft.fontSize} onChange={event => update('fontSize', Number(event.target.value))}>
                      {fontSizes.map(size => <option key={size} value={size}>{`${size} px`}</option>)}
                    </select>
                  </SettingRow>
                </div>
              </section>
              <section className="settings-section" aria-labelledby="settings-playback-title">
                <h2 id="settings-playback-title" className="text-ui-lg">播放</h2>
                <div className="settings-group">
                  <SettingRow id="settings-volume" title="音量" description="只调整这台设备的播放音量。" field>
                    <div className="settings-volume-control">
                      <input id="settings-volume" aria-describedby="settings-volume-description" type="range" min="0" max="100" value={draft.volume} aria-label="音量滑块" onChange={event => update('volume', Number(event.target.value))} />
                      <div className="settings-volume-input-control">
                        <input
                          className="settings-volume-input"
                          type="number"
                          min="0"
                          max="100"
                          step="1"
                          inputMode="numeric"
                          value={draft.volume}
                          aria-label="音量百分比"
                          onChange={(event) => {
                            const value = Number(event.target.value)
                            if (Number.isFinite(value))
                              update('volume', Math.min(100, Math.max(0, Math.round(value))))
                          }}
                        />
                        <span className="settings-volume-unit" aria-hidden="true">%</span>
                      </div>
                    </div>
                  </SettingRow>
                  <SettingRow id="settings-muted" title="静音" description="暂时关闭这台设备的声音。">
                    <input id="settings-muted" aria-describedby="settings-muted-description" className="settings-switch" type="checkbox" role="switch" checked={draft.muted} onChange={event => update('muted', event.target.checked)} />
                  </SettingRow>
                  <SettingRow id="settings-reminder" title="自己点的歌开播时提醒" description="轮到你点的歌时，在页面上显示一条通知。">
                    <input id="settings-reminder" aria-describedby="settings-reminder-description" className="settings-switch" type="checkbox" role="switch" checked={draft.reminderEnabled} onChange={event => update('reminderEnabled', event.target.checked)} />
                  </SettingRow>
                  {systemNotificationsSupported() && (
                    <SettingRow id="settings-system-notifications" title="页面在后台时，用系统通知提醒" description={systemNotifications && Notification.permission !== 'granted' ? '浏览器现在不允许显示通知，关掉再打开可以重新申请。' : '轮到你点的歌时，在这台设备上显示系统通知。'}>
                      <input id="settings-system-notifications" aria-describedby="settings-system-notifications-description" className="settings-switch" type="checkbox" role="switch" checked={draft.systemNotifications} disabled={!draft.reminderEnabled} onChange={event => toggleSystemNotifications(event.target.checked)} />
                    </SettingRow>
                  )}
                </div>
              </section>
              <section className="settings-section" aria-labelledby="settings-performance-title">
                <h2 id="settings-performance-title" className="text-ui-lg">性能</h2>
                <div className="settings-group">
                  <SettingRow id="settings-performance" title="性能模式" description={levelNames[draft.level].description}>
                    <select id="settings-performance" aria-describedby="settings-performance-description" value={draft.level} onChange={event => update('level', performanceLevels.find(value => value === event.target.value) ?? draft.level)}>
                      {performanceLevels.map(value => <option key={value} value={value}>{levelNames[value].name}</option>)}
                    </select>
                  </SettingRow>
                  <SettingRow id="settings-reduced-motion" title="减少动态效果" description="关闭界面动画和歌词的平滑滚动。">
                    <input id="settings-reduced-motion" aria-describedby="settings-reduced-motion-description" className="settings-switch" type="checkbox" role="switch" checked={draft.reducedMotion} onChange={event => update('reducedMotion', event.target.checked)} />
                  </SettingRow>
                </div>
              </section>
              <section className="settings-section" aria-labelledby="settings-history-title">
                <h2 id="settings-history-title" className="text-ui-lg">历史记录</h2>
                <div className="settings-group">
                  <SettingRow id="settings-search-history" title="保存搜索记录" description="在这台设备上保留最近搜索的内容。">
                    <input id="settings-search-history" aria-describedby="settings-search-history-description" className="settings-switch" type="checkbox" role="switch" checked={draft.searchesEnabled} onChange={event => update('searchesEnabled', event.target.checked)} />
                  </SettingRow>
                  <SettingRow id="settings-search-limit" title="搜索记录数量" description="最多保留的搜索记录条数。">
                    <select id="settings-search-limit" aria-describedby="settings-search-limit-description" value={draft.searchLimit} disabled={!draft.searchesEnabled} onChange={event => update('searchLimit', Number(event.target.value))}>
                      {searchLimits.map(limit => <option key={limit} value={limit}>{`${limit} 条`}</option>)}
                    </select>
                  </SettingRow>
                  <SettingRow id="settings-play-history" title="记录播放历史" description="把房间播放过的歌曲保存在这台设备上。">
                    <input id="settings-play-history" aria-describedby="settings-play-history-description" className="settings-switch" type="checkbox" role="switch" checked={draft.playsEnabled} onChange={event => update('playsEnabled', event.target.checked)} />
                  </SettingRow>
                </div>
              </section>
            </div>
          </fieldset>
        </div>
        <footer className="settings-page-footer">
          <p className="settings-save-status text-ui-caption muted" role="status">{dirty ? '有未保存的更改' : '所有更改已保存'}</p>
          <button type="submit" className="button-primary" disabled={!dirty || saving} aria-busy={saving}>{saving ? '正在保存…' : '保存'}</button>
        </footer>
      </form>
    </section>
  )
}
