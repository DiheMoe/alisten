import { Music2 } from 'lucide-react'
import { useState } from 'react'

export function Artwork({ src, title, className = '' }: { src?: string, title: string, className?: string }) {
  const [failedSrc, setFailedSrc] = useState<string>()
  return (
    <div className={`artwork ${className}`}>
      {src && src !== failedSrc
        ? <img src={src} alt={title} loading="lazy" decoding="async" onError={() => setFailedSrc(src)} />
        : <Music2 size={40} strokeWidth={1.2} aria-hidden="true" />}
    </div>
  )
}
