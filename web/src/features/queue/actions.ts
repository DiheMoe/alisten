import type { PlayMode, Track } from '@/core/realtime'
import { errorMessage } from '@/core/errors'
import { RequestError } from '@/core/realtime'
import { roomStore } from '@/core/room'
import { bookTitle } from '@/shared/lib/book-title'
import { notificationStore } from '@/shared/ui'

async function run(action: () => Promise<string | null>): Promise<void> {
  try {
    const message = await action()
    if (message)
      notificationStore.success(message)
  }
  catch (error) {
    notificationStore.error(errorMessage(error))
  }
}

// 返回 false 时，失败原因已经通过通知显示。
export async function enqueue(track: Track): Promise<boolean> {
  try {
    const { entry } = await roomStore.requests.queueAdd({ track })
    notificationStore.success(`已点歌${bookTitle(entry.meta.title)}`)
    return true
  }
  catch (error) {
    // 点了认得出却不能点的 B 站链接时，INVALID_ARGUMENT 的 message 就是写给点歌人的原因。
    notificationStore.error(track.source === 'link' && error instanceof RequestError && error.code === 'INVALID_ARGUMENT' ? error.message : errorMessage(error))
    return false
  }
}

export function voteSkip(): Promise<void> {
  return run(async () => {
    const { votes, required, skipped } = await roomStore.requests.playbackVoteSkip()
    return skipped ? '已切歌' : `已投票切歌（${votes}/${required}）`
  })
}

export function likeEntry(entryId: string, title: string): Promise<void> {
  return run(async () => {
    await roomStore.requests.queueLike({ entryId })
    return `已点赞${bookTitle(title)}`
  })
}

export function removeEntry(entryId: string, title: string): Promise<void> {
  return run(async () => {
    await roomStore.requests.queueRemove({ entryId })
    return `已移除${bookTitle(title)}`
  })
}

export function setPlayMode(playMode: PlayMode): Promise<void> {
  return run(async () => {
    await roomStore.requests.settingsSetPlayMode({ playMode })
    return null
  })
}
