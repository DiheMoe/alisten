import { ListMusic, MessageCircle, SkipForward, Volume2, VolumeX } from 'lucide-react'
import { useEffect, useRef } from 'react'
import { roomStore } from '@/core/room'
import { sourceName } from '@/features/queue'
import { formatDuration } from '@/shared/lib/time'
import { useStore } from '@/shared/lib/use-store'
import { Artwork, AsyncIconButton, IconButton, VolumeInput } from '@/shared/ui'
import { playerStore } from './player-store'
import { volumeStore } from './volume-store'
import './playback.css'

const progressIntervalMs = 100

function PlaybackProgress({ durationMs }: { durationMs: number }) {
  const fillRef = useRef<HTMLDivElement>(null)
  const timeRef = useRef<HTMLSpanElement>(null)
  const meterRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    let lastSecond = -1
    let lastUpdate = Number.NEGATIVE_INFINITY
    const update = (positionMs: number) => {
      const clamped = Math.max(0, Math.min(durationMs, positionMs))
      if (fillRef.current)
        fillRef.current.style.transform = `scaleX(${durationMs ? clamped / durationMs : 0})`
      const second = Math.floor(clamped / 1000)
      if (second === lastSecond)
        return
      lastSecond = second
      if (timeRef.current)
        timeRef.current.textContent = `${formatDuration(clamped)} / ${formatDuration(durationMs)}`
      meterRef.current?.setAttribute('aria-valuenow', String(second))
    }
    update(playerStore.positionMs())
    return playerStore.frames.on((positionMs) => {
      const now = performance.now()
      if (now - lastUpdate < progressIntervalMs)
        return
      lastUpdate = now
      update(positionMs)
    })
  }, [durationMs])
  return (
    <>
      <span ref={timeRef} className="player-time text-ui-xs">0:00 / 0:00</span>
      <div className="playback-progress">
        <div ref={meterRef} className="progress-track" role="progressbar" aria-label="播放进度" aria-valuemin={0} aria-valuemax={Math.floor(durationMs / 1000)} aria-valuenow={0}>
          <div ref={fillRef} className="progress-fill" />
        </div>
      </div>
    </>
  )
}

export function PlayerBar({ onSkip, onLyrics, onQueue, onChat, queueOpen, chatOpen, canOpenPanels }: {
  onSkip: () => Promise<void>
  onLyrics: () => void
  onQueue: () => void
  onChat: () => void
  queueOpen: boolean
  chatOpen: boolean
  canOpenPanels: boolean
}) {
  const nowPlaying = useStore(roomStore, s => s.room?.nowPlaying ?? null)
  const connected = useStore(roomStore, s => s.status === 'open')
  const volume = useStore(volumeStore, s => s.volume)
  const muted = useStore(volumeStore, s => s.muted)
  const meta = nowPlaying?.entry.meta
  const artist = meta?.artist || '未知艺术家'
  const source = nowPlaying ? sourceName(nowPlaying.entry.track) : undefined
  const note = nowPlaying?.entry.by ? `${nowPlaying.entry.by.name} 点的歌` : undefined
  return (
    <section className="player-bar" aria-label="播放器">
      <button type="button" className="player-artwork" onClick={onLyrics} aria-label="查看歌词">
        <Artwork src={meta?.coverUrl} title={meta?.title ?? '暂无歌曲'} />
      </button>
      <div className="player-track">
        <div className="player-song">
          <span className="song-copy">
            <strong className="text-ui-lg truncate">{meta?.title ?? '暂无歌曲'}</strong>
            <span className="text-ui-caption muted truncate">
              {[artist, source, note].filter(Boolean).join(' · ')}
            </span>
          </span>
        </div>
        <PlaybackProgress key={nowPlaying?.entry.id} durationMs={nowPlaying?.durationMs ?? 0} />
      </div>
      <div className="player-right">
        <div className="player-center">
          <AsyncIconButton icon={SkipForward} label="投票切歌" disabled={!nowPlaying || !connected} onAction={onSkip} />
        </div>
        <div className="player-tools">
          <IconButton icon={muted ? VolumeX : Volume2} label={muted ? '取消静音' : '静音'} onClick={() => volumeStore.setMuted(!muted)} />
          <VolumeInput
            volume={volume}
            sliderValue={muted ? 0 : volume}
            onCommit={(next) => {
              volumeStore.setVolume(next)
              if (muted && next > 0)
                volumeStore.setMuted(false)
            }}
          />
          <IconButton icon={MessageCircle} label="房间聊天" className="player-panel-toggle" disabled={!canOpenPanels} aria-pressed={chatOpen} onClick={onChat} />
          <IconButton icon={ListMusic} label="播放列表" className="player-panel-toggle" disabled={!canOpenPanels} aria-pressed={queueOpen} onClick={onQueue} />
        </div>
      </div>
    </section>
  )
}
