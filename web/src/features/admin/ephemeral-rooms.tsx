import { api } from '@/core/api'
import { useResource } from '@/shared/lib/use-resource'
import { InlineTextField, Skeleton } from '@/shared/ui'
import { adminStore } from './admin-store'
import { capacityRangeError, parseCapacity } from './room-capacity'
import { LoadFailed, SectionHead } from './section-head'

export function EphemeralRooms() {
  const capacity = useResource(() => adminStore.attempt(() => api.getSite()).then(site => site.ephemeralRoomCapacity))
  const save = async (draft: string) => {
    const next = parseCapacity(draft)
    if (next === null)
      throw new Error(capacityRangeError)
    await adminStore.attempt(() => api.adminSetEphemeralRoomCapacity({ capacity: next }))
    capacity.set(next)
  }
  return (
    <section className="admin-section" aria-labelledby="admin-ephemeral-rooms-title">
      <SectionHead id="admin-ephemeral-rooms-title" title="临时房间" />
      {capacity.state.status === 'loading'
        ? <Skeleton rows={1} />
        : capacity.state.status === 'failed'
          ? <LoadFailed message={`暂时无法获取临时房间最多人数。${capacity.state.error}`} onRetry={capacity.reload} />
          : (
              <div className="section-group">
                <div className="section-row">
                  <div className="section-row-copy">
                    <span className="section-row-title">最多人数</span>
                    <span className="section-row-description">只影响之后新建的临时房间。</span>
                  </div>
                  <InlineTextField label="临时房间最多人数" type="number" value={String(capacity.state.value)} onSave={save} />
                </div>
              </div>
            )}
    </section>
  )
}
