import { useEffect, useEffectEvent, useRef } from 'react'
import { playerStore } from './player-store'

export function AudioEngine({ onNextTrack }: { onNextTrack: () => void }) {
  const activeRef = useRef<HTMLAudioElement>(null)
  const standbyRef = useRef<HTMLAudioElement>(null)
  const nextTrack = useEffectEvent(onNextTrack)
  useEffect(() => {
    playerStore.attach(activeRef.current!, standbyRef.current!, () => nextTrack())
    return () => playerStore.detach()
  }, [])
  return (
    <>
      <audio ref={activeRef} preload="auto" />
      <audio ref={standbyRef} preload="auto" />
    </>
  )
}
