import type { ImportMyPlatformPlaylistResponse, ImportMyPlaylistResponse, ResolveMyPlaylistLinkResponse } from '@/core/api'
import type { ErrorTexts } from '@/core/errors'
import type { ImportedTrack, MyPlaylist, Source, Track } from '@/core/realtime'
import { api } from '@/core/api'
import { errorMessage } from '@/core/errors'
import { profileStore } from '@/features/settings'
import { Store } from '@/shared/lib/store'
import { notificationStore } from '@/shared/ui'

export function trackKey(track: Track): string {
  return `${track.source}:${track.id}`
}

function membershipKey(playlistId: string, track: Track): string {
  return `${playlistId} ${trackKey(track)}`
}

interface PlaylistsState {
  // 游客，或者还没有读到时为 null。
  playlists: MyPlaylist[] | null
  // 最近一次读取失败的原因，成功读取后为 null。
  error: string | null
  // 歌单最近一次读取成功的时间，相对日期（今天、昨天）以它为准：窗口重新获得焦点会重新读取，跨过午夜后标签随之更新。
  loadedAt: number
  // 曲目 → 包含它的歌单 id，只有读过的曲目才有。正在同步的歌单是最后一次勾选的结果，同步负责把服务端改成一致。
  memberships: ReadonlyMap<string, ReadonlySet<string>>
}

const empty: PlaylistsState = { playlists: null, error: null, loadedAt: 0, memberships: new Map() }

const nameTexts = { INVALID_ARGUMENT: '歌单名要 1 到 50 个字。' }
const fileTexts = { INVALID_ARGUMENT: '文件里有不符合要求的内容，没有导入。' }
// 接口只收 1 到 2048 字节的文本：太长的这里不发出，对话框不提交空的，INVALID_ARGUMENT 就只剩认不出链接，这时的 message 是写给用户的。
const maxLinkBytes = 2048
const linkTexts: ErrorTexts = { INVALID_ARGUMENT: message => message }

class PlaylistsStore extends Store<PlaylistsState> {
  #account: string | null = null
  // 只有最后发起的读取能写入：换了账号，或者之后又读过一次时，旧的结果作废。
  #generation = 0
  #membershipReads = new Map<string, object>()
  #syncing = new Map<string, Promise<void>>()

  constructor() {
    super(empty)
  }

  start(): void {
    profileStore.addEventListener('change', this.#onProfile)
    window.addEventListener('focus', this.#onFocus)
    this.#onProfile()
  }

  // 失败时只设置 error，不抛出。
  load(): Promise<void> {
    return this.#load(++this.#generation)
  }

  // 读取包含这首歌的歌单，写入 memberships；没读到时返回 false。读到了却已被之后的读取取代时也返回 true，由后来的读取写入。
  async containing(track: Track): Promise<boolean> {
    const account = this.#account
    const key = trackKey(track)
    const read = {}
    this.#membershipReads.set(key, read)
    try {
      const { playlistIds } = await api.listMyTrackPlaylists({ track })
      if (this.#account === account && this.#membershipReads.get(key) === read)
        this.#applyMemberships(track, playlistIds)
      return true
    }
    catch {
      return false
    }
    finally {
      if (this.#membershipReads.get(key) === read)
        this.#membershipReads.delete(key)
    }
  }

  setMembership(playlist: MyPlaylist, track: Track, included: boolean): Promise<void> {
    const key = membershipKey(playlist.id, track)
    const saved = this.#includes(playlist.id, track)
    this.#setIncluded(playlist.id, track, included)
    let sync = this.#syncing.get(key)
    if (!sync) {
      sync = this.#sync(playlist.id, track, saved)
      this.#syncing.set(key, sync)
      void sync.finally(() => {
        if (this.#syncing.get(key) !== sync)
          return
        this.#syncing.delete(key)
        // 同步之前发出的读取可能带着旧的勾选返回，重新读一次让它们作废；请求超时但服务端已生效时，也以服务端为准。
        void this.containing(track)
        void this.load()
      })
    }
    return sync
  }

  async create(name: string): Promise<MyPlaylist | null> {
    try {
      const playlist = await api.createMyPlaylist({ name })
      this.#added(playlist)
      return playlist
    }
    catch (error) {
      notificationStore.error(errorMessage(error, nameTexts))
      return null
    }
  }

  async importFile(name: string, tracks: ImportedTrack[]): Promise<ImportMyPlaylistResponse | null> {
    try {
      const imported = await api.importMyPlaylist({ name, tracks })
      this.#added(imported.playlist)
      return imported
    }
    catch (error) {
      notificationStore.error(errorMessage(error, fileTexts))
      return null
    }
  }

  async resolveLink(link: string): Promise<ResolveMyPlaylistLinkResponse | null> {
    if (new TextEncoder().encode(link).length > maxLinkBytes) {
      notificationStore.error('文字太长了，只粘贴歌单的链接或分享文字就好。')
      return null
    }
    try {
      return await api.resolveMyPlaylistLink({ link })
    }
    catch (error) {
      notificationStore.error(errorMessage(error, linkTexts))
      return null
    }
  }

  async importPlatform(name: string, source: Source, playlistId: string): Promise<ImportMyPlatformPlaylistResponse | null> {
    try {
      const imported = await api.importMyPlatformPlaylist({ name, source, playlistId })
      this.#added(imported.playlist)
      return imported
    }
    catch (error) {
      notificationStore.error(errorMessage(error, nameTexts))
      return null
    }
  }

  async rename(playlist: MyPlaylist, name: string): Promise<boolean> {
    try {
      await api.renameMyPlaylist(playlist.id, { name })
      await this.load()
      return true
    }
    catch (error) {
      notificationStore.error(errorMessage(error, nameTexts))
      return false
    }
  }

  async remove(playlist: MyPlaylist): Promise<boolean> {
    try {
      await api.deleteMyPlaylist(playlist.id)
      this.setState({ playlists: (this.state.playlists ?? []).filter(p => p.id !== playlist.id) })
      void this.load()
      return true
    }
    catch (error) {
      notificationStore.error(errorMessage(error))
      return false
    }
  }

  async removeItems(playlist: MyPlaylist, tracks: Track[]): Promise<boolean> {
    try {
      await api.removeMyPlaylistItems(playlist.id, { tracks })
      void this.load()
      return true
    }
    catch (error) {
      notificationStore.error(errorMessage(error))
      return false
    }
  }

  // 返回新加入的首数，to 里已经有的歌不算；失败时返回 null。
  async copyItems(from: MyPlaylist, to: MyPlaylist, tracks: Track[]): Promise<number | null> {
    try {
      const { added } = await api.copyMyPlaylistItems(to.id, { from: from.id, tracks })
      void this.load()
      return added
    }
    catch (error) {
      notificationStore.error(`没能保存到“${to.name}”：${errorMessage(error)}`)
      return null
    }
  }

  // 没有 before 时移到最后。
  async moveItems(playlist: MyPlaylist, tracks: Track[], before?: Track): Promise<boolean> {
    try {
      await api.moveMyPlaylistItems(playlist.id, before ? { tracks, before } : { tracks })
      void this.load()
      return true
    }
    catch (error) {
      notificationStore.error(errorMessage(error))
      return false
    }
  }

  // 先放进列表再重新读取：打开新歌单时，列表里要已经有它，否则详情页会以为它被删掉了。
  #added(playlist: MyPlaylist): void {
    this.setState({ playlists: [...(this.state.playlists ?? []), playlist] })
    void this.load()
  }

  // 请求进行中又改了勾选时，等它完成之后按最新的勾选再发；失败时恢复成服务端的状态，不再发出。
  async #sync(playlistId: string, track: Track, saved: boolean): Promise<void> {
    const account = this.#account
    while (this.#account === account && this.#includes(playlistId, track) !== saved) {
      const included = this.#includes(playlistId, track)
      try {
        if (included)
          await api.addMyPlaylistItem(playlistId, { track })
        else
          await api.removeMyPlaylistItems(playlistId, { tracks: [track] })
        saved = included
      }
      catch (error) {
        if (this.#account !== account)
          return
        this.#setIncluded(playlistId, track, saved)
        notificationStore.error(errorMessage(error))
        return
      }
    }
  }

  async #load(generation: number): Promise<void> {
    try {
      const { items } = await api.listMyPlaylists()
      if (generation === this.#generation)
        this.setState({ playlists: items, error: null, loadedAt: Date.now() })
    }
    catch (error) {
      if (generation === this.#generation)
        this.setState({ error: errorMessage(error) })
    }
  }

  // 正在同步的歌单以本地为准：读到的可能是同步完成之前的状态。
  #applyMemberships(track: Track, playlistIds: readonly string[]): void {
    const syncing = (id: string) => this.#syncing.has(membershipKey(id, track))
    const next = new Set(playlistIds.filter(id => !syncing(id)))
    for (const id of this.state.memberships.get(trackKey(track)) ?? []) {
      if (syncing(id))
        next.add(id)
    }
    this.setState({ memberships: new Map(this.state.memberships).set(trackKey(track), next) })
  }

  #includes(playlistId: string, track: Track): boolean {
    return this.state.memberships.get(trackKey(track))?.has(playlistId) ?? false
  }

  #setIncluded(playlistId: string, track: Track, included: boolean): void {
    const ids = new Set(this.state.memberships.get(trackKey(track)))
    if (included)
      ids.add(playlistId)
    else
      ids.delete(playlistId)
    this.setState({ memberships: new Map(this.state.memberships).set(trackKey(track), ids) })
  }

  #onProfile = (): void => {
    const username = profileStore.state.account?.username ?? null
    if (username === this.#account)
      return
    this.#account = username
    this.#generation++
    this.#membershipReads.clear()
    this.#syncing.clear()
    this.setState(empty)
    if (username)
      void this.load()
  }

  #onFocus = (): void => {
    if (this.#account)
      void this.load()
  }
}

export const playlistsStore = new PlaylistsStore()
