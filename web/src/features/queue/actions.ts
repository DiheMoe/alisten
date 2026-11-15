import type { PlayMode } from '@/core/realtime'
import { errorMessage } from '@/core/errors'
import { roomStore } from '@/core/room'
import { bookTitle } from '@/shared/lib/book-title'
import { notificationStore } from '@/shared/lib/notification-store'

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
