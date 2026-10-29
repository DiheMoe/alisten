export function Skeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="skeleton-list" role="status" aria-label="正在加载">
      {Array.from({ length: rows }, (_, i) => (
        <div className="skeleton-row" key={i}>
          <span />
          <div>
            <span />
            <span />
          </div>
        </div>
      ))}
    </div>
  )
}
