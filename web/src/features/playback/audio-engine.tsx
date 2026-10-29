import { useEffect, useEffectEvent, useRef } from 'react'
import { playerStore } from './player-store'

export function AudioEngine({ onNextTrack }: { onNextTrack: () => void }) {
  const audioRef = useRef<HTMLAudioElement>(null)
  const nextTrack = useEffectEvent(onNextTrack)
  useEffect(() => {
    playerStore.attach(audioRef.current!, () => nextTrack())
    return () => playerStore.detach()
  }, [])
  return <audio ref={audioRef} preload="auto" />
}
