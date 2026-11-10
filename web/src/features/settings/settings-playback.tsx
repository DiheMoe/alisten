import { Volume2, VolumeX } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { reminderStore, systemNotificationsSupported, volumeStore } from '@/features/playback'
import { useSettingSave } from '@/shared/lib/use-setting-save'
import { useStore } from '@/shared/lib/use-store'
import { SaveStatus } from '@/shared/ui'
import { NotificationPermissionRequest } from './notification-permission'
import { SettingRow, SettingSwitch, SwitchRow } from './setting-row'
import { notificationHint, notificationRefusal } from './settings-labels'
import { VolumeInput } from './volume-input'

function SystemNotificationsRow() {
  const reminderEnabled = useStore(reminderStore, s => s.enabled)
  const saved = useStore(reminderStore, s => s.systemNotifications)
  const setting = useSettingSave(saved, (on: boolean) => reminderStore.updateSettings({ enabled: reminderStore.state.enabled, systemNotifications: on }))
  const [permissionRequest] = useState(() => new NotificationPermissionRequest())
  const { permission, waiting, refused } = useStore(permissionRequest, s => s)
  const hint = notificationHint(permission)
  const error = refused ? notificationRefusal(permission) : setting.state.error
  const id = 'settings-system-notifications'
  // 浏览器答复之后“授权”按钮不再显示，焦点如果在这个按钮上，就会掉到 body。这时把焦点交给同一行的开关。
  const authorizeShownRef = useRef(hint.authorize)
  useEffect(() => {
    const shown = authorizeShownRef.current
    authorizeShownRef.current = hint.authorize
    if (shown && !hint.authorize && document.activeElement === document.body)
      document.getElementById(id)?.focus()
  }, [hint.authorize])
  // 打开之前先拿到授权，申请要在点击的同一个事件里发起。等待授权时开关显示为开，再点一次就是放弃。
  const change = async (on: boolean) => {
    if (!on) {
      permissionRequest.abandon()
      setting.change(false)
      return
    }
    if (Notification.permission === 'granted' || await permissionRequest.request())
      setting.change(true)
  }
  return (
    <SettingRow
      id={id}
      title="页面在后台时用系统通知"
      description={hint.description}
      error={error}
      status={<SaveStatus status={waiting ? 'saving' : setting.state.status} version={setting.savedVersion} savingText="等待浏览器授权…" />}
    >
      {hint.authorize && <button type="button" className="button-secondary settings-button" disabled={!reminderEnabled} onClick={() => void change(true)}>授权</button>}
      <SettingSwitch id={id} checked={waiting || setting.value} error={error} disabled={!reminderEnabled} onChange={on => void change(on)} />
    </SettingRow>
  )
}

export function PlaybackSettings() {
  const volume = useStore(volumeStore, s => s.volume)
  const muted = useStore(volumeStore, s => s.muted)
  const reminderEnabled = useStore(reminderStore, s => s.enabled)
  const VolumeIcon = muted || volume === 0 ? VolumeX : Volume2
  return (
    <>
      <h2 className="settings-title">播放与提醒</h2>
      <div className="section-group">
        <SettingRow id="settings-volume" title="音量" description="只影响这台设备">
          <VolumeIcon className="settings-volume-icon" size={18} strokeWidth={1.8} aria-hidden="true" />
          <VolumeInput id="settings-volume" className="settings-volume-control" volume={volume} onCommit={next => volumeStore.setVolume(next)} />
        </SettingRow>
        <SwitchRow id="settings-muted" title="静音" description="暂时关闭这台设备的声音" checked={muted} onSave={on => volumeStore.setMuted(on)} />
        <SwitchRow
          id="settings-reminder"
          title="自己点的歌开播时提醒"
          description="在页面上显示一条通知"
          checked={reminderEnabled}
          onSave={on => reminderStore.updateSettings({ enabled: on, systemNotifications: reminderStore.state.systemNotifications })}
        />
        {systemNotificationsSupported() && <SystemNotificationsRow />}
      </div>
    </>
  )
}
