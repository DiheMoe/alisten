import { playlistNameMaxLength } from './playlists-store'

export function PlaylistNameField({ value, onChange, ...focus }: { 'value': string, 'onChange': (value: string) => void, 'autoFocus'?: boolean, 'data-autofocus'?: boolean }) {
  return (
    <label>
      歌单名
      <input {...focus} required maxLength={playlistNameMaxLength} value={value} onChange={event => onChange(event.target.value)} />
    </label>
  )
}
