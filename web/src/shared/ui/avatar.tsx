import { useState } from 'react'

export function Avatar({ hash, name, className = '' }: { hash: string, name: string, className?: string }) {
  const [failedHash, setFailedHash] = useState<string>()
  return (
    <span className={`avatar ${className}`} aria-hidden="true">
      {hash && hash !== failedHash
        ? <img src={`https://www.gravatar.com/avatar/${hash}?s=96&d=404`} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => setFailedHash(hash)} />
        : Array.from(name.trim())[0] ?? '?'}
    </span>
  )
}
