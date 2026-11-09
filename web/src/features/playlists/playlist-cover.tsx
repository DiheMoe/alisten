import { Music2, Star } from 'lucide-react'
import { useState } from 'react'

export function FavoritesMark({ size = 14 }: { size?: number }) {
  return <Star className="playlist-favorites-mark" size={size} fill="currentColor" strokeWidth={1.5} aria-hidden="true" />
}

function FilledStar() {
  return <Star fill="currentColor" strokeWidth={1.5} aria-hidden="true" />
}

export function PlaylistCover({ cover, favorites, size }: { cover: string | null, favorites: boolean, size: 'sm' | 'md' | 'lg' }) {
  const [failedSrc, setFailedSrc] = useState<string>()
  if (!cover || cover === failedSrc) {
    return (
      <span className={`playlist-cover playlist-cover-${size}`} data-favorites={favorites || undefined}>
        {favorites ? <FilledStar /> : <Music2 strokeWidth={1.5} aria-hidden="true" />}
      </span>
    )
  }
  return (
    <span className={`playlist-cover playlist-cover-${size}`}>
      <img src={cover} alt="" loading="lazy" decoding="async" onError={() => setFailedSrc(cover)} />
    </span>
  )
}
