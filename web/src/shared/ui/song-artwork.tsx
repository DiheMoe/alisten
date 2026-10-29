import { ExternalLink } from 'lucide-react'
import { bookTitle } from '@/shared/lib/book-title'
import { Artwork } from './artwork'

function safeHref(href: string | undefined): string | undefined {
  if (!href)
    return undefined
  try {
    const url = new URL(href)
    return url.protocol === 'https:' || url.protocol === 'http:' ? url.href : undefined
  }
  catch {
    return undefined
  }
}

export function SongArtwork({ coverUrl, title, href, sourceName = '音源' }: { coverUrl: string, title: string, href?: string, sourceName?: string }) {
  const link = safeHref(href)
  if (!link)
    return <Artwork src={coverUrl} title={title} />
  return (
    <a className="song-artwork" href={link} target="_blank" rel="noopener noreferrer" aria-label={`在${sourceName}打开${bookTitle(title)}（新标签页）`} title={`在${sourceName}打开（新标签页）`}>
      <Artwork src={coverUrl} title={title} />
      <ExternalLink size={16} strokeWidth={1.7} className="song-artwork-icon" aria-hidden="true" />
    </a>
  )
}
