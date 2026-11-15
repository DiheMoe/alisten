import { useEffect, useState } from 'react'
import { Submission } from './submission'

export function useSubmission() {
  const [busy, setBusy] = useState(false)
  const [submission] = useState(() => new Submission(setBusy))
  useEffect(() => {
    submission.open()
    return () => submission.close()
  }, [submission])
  return { busy, submit: (task: () => Promise<void>) => submission.run(task) }
}
