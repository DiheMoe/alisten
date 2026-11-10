import type { ImportedTrack, MyPlaylistItem, Source } from '@/core/realtime'

const format = 'alisten.playlist'
export const importLimit = 1000
const maxDurationMs = 86_400_000
const sources: ReadonlySet<string> = new Set<Source>(['netease', 'qq', 'bilibili', 'link'])
const notPlaylistFile = '这个文件不是导出的歌单。'

export interface PlaylistFile {
  name: string
  tracks: ImportedTrack[]
  // 文件里超过 importLimit 首，只取了前面的。
  truncated: boolean
}

export function exportPlaylist(name: string, items: readonly MyPlaylistItem[], now: number): string {
  const tracks = items.map(({ track, meta }) => ({
    source: track.source,
    id: track.id,
    title: meta.title,
    artist: meta.artist,
    album: meta.album,
    durationMs: meta.durationMs,
    coverUrl: meta.coverUrl,
    pageUrl: meta.pageUrl,
  }))
  return JSON.stringify({ format, name, exportedAt: new Date(now).toISOString(), tracks }, null, 2)
}

// Windows 的文件名不能含 \ / : * ? " < > | 和控制字符，其他系统也不能含 /。
export function exportFileName(name: string): string {
  return `${name.replace(/[\\/:*?"<>|\p{Cc}]/gu, '_')}.json`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

// 文件可以随意改写。元数据的类型不对时当作没有，由服务端决定用不用。音源和 ID 不对就认不出是哪首歌，整个文件都不导入。
function importedTrack(entry: unknown): ImportedTrack | null {
  if (!isRecord(entry) || typeof entry.source !== 'string' || !sources.has(entry.source) || typeof entry.id !== 'string' || entry.id === '')
    return null
  const { durationMs } = entry
  const imported: ImportedTrack = {
    track: { source: entry.source as Source, id: entry.id },
    title: text(entry.title) ?? '',
    durationMs: typeof durationMs === 'number' && Number.isInteger(durationMs) && durationMs >= 0 && durationMs <= maxDurationMs ? durationMs : 0,
  }
  for (const key of ['artist', 'album', 'coverUrl', 'pageUrl'] as const) {
    const value = text(entry[key])
    if (value !== undefined)
      imported[key] = value
  }
  return imported
}

// 返回读到的歌单，或者给用户看的错误信息。
export function parsePlaylistFile(content: string): PlaylistFile | string {
  let data: unknown
  try {
    data = JSON.parse(content)
  }
  catch {
    return notPlaylistFile
  }
  if (!isRecord(data) || data.format !== format || !Array.isArray(data.tracks))
    return notPlaylistFile
  if (data.tracks.length === 0)
    return '这个歌单文件里没有歌。'
  const tracks: ImportedTrack[] = []
  for (const [index, entry] of data.tracks.slice(0, importLimit).entries()) {
    const track = importedTrack(entry)
    if (!track)
      return `文件里的第 ${index + 1} 首歌缺少音源或 ID，没有导入。`
    tracks.push(track)
  }
  return { name: text(data.name) ?? '', tracks, truncated: data.tracks.length > importLimit }
}
