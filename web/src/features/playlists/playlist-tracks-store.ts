import type { MovePlan } from './track-order'
import type { MyPlaylist, MyPlaylistItem, Track } from '@/core/realtime'
import type { Selection } from '@/shared/lib/selection'
import { api } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { noSelection, retain } from '@/shared/lib/selection'
import { Store } from '@/shared/lib/store'
import { playlistsStore, trackKey } from './playlists-store'
import { TrackEdits } from './track-edits'

interface TracksState {
  // 还没读到时为 null。
  items: MyPlaylistItem[] | null
  failed: string | null
  selection: Selection
}

export function keysOf(items: readonly MyPlaylistItem[]): string[] {
  return items.map(item => trackKey(item.track))
}

// 一个歌单详情页里的曲目和选择。每次打开页面新建一个，所以换歌单时不带着上一个歌单的曲目。
export class PlaylistTracksStore extends Store<TracksState> {
  readonly #id: string
  readonly #edits = new TrackEdits(() => void this.load())
  #reads = 0

  constructor(id: string) {
    super({ items: null, failed: null, selection: noSelection })
    this.#id = id
  }

  // 只用最后一次发起的读取。修改进行中读到的列表也不用，原因见 TrackEdits。
  async load(): Promise<void> {
    const read = ++this.#reads
    const usable = this.#edits.read()
    try {
      const { items } = await api.listMyPlaylistItems(this.#id)
      if (read === this.#reads && usable())
        this.setState({ items, failed: null, selection: retain(this.state.selection, keysOf(items)) })
    }
    catch (error) {
      if (read === this.#reads && usable())
        this.setState({ failed: errorMessage(error) })
    }
  }

  retry(): void {
    this.setState({ failed: null })
    void this.load()
  }

  select(selection: Selection): void {
    this.setState({ selection })
  }

  // 先在本地换好顺序再请求，失败时回到这次移动之前的顺序。
  move(playlist: MyPlaylist, plan: MovePlan<string>): void {
    const current = this.state.items
    if (!current)
      return
    const byKey = new Map(current.map(item => [trackKey(item.track), item]))
    const trackOf = (key: string) => byKey.get(key)!.track
    const tracks = plan.moved.map(trackOf)
    const before = plan.before === undefined ? undefined : trackOf(plan.before)
    this.setState({ items: plan.order.map(key => byKey.get(key)!) })
    void this.#edits.run(() => playlistsStore.moveItems(playlist, tracks, before), () => this.setState({ items: current }))
  }

  // 先在本地移出再请求，移出的歌同时从选择里去掉。失败时曲目和选择都回到这次移出之前。返回的 Promise 在这次移出有了结果之后完成。
  remove(playlist: MyPlaylist, tracks: Track[]): Promise<void> {
    const current = this.state.items
    if (!current)
      return Promise.resolve()
    const gone = new Set(tracks.map(trackKey))
    const rest = current.filter(item => !gone.has(trackKey(item.track)))
    const selection = this.state.selection
    this.setState({ items: rest, selection: retain(selection, keysOf(rest)) })
    return this.#edits.run(() => playlistsStore.removeItems(playlist, tracks), () => this.setState({ items: current, selection }))
  }
}
