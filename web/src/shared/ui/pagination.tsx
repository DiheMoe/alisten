import type { ReactNode } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { pageItems } from '@/shared/lib/page-items'

// 到头时用 aria-disabled 而不是 disabled，因为被禁用的按钮会丢掉焦点，连按“下一页”翻到最后一页的键盘用户就找不到自己在哪了。
function StepButton({ label, enabled, onClick, children }: { label: string, enabled: boolean, onClick: () => void, children: ReactNode }) {
  return (
    <button
      type="button"
      className="pagination-step"
      aria-label={label}
      title={label}
      aria-disabled={!enabled || undefined}
      onClick={() => {
        if (enabled)
          onClick()
      }}
    >
      {children}
    </button>
  )
}

export function Pagination({ page, totalPages, onPage, summary }: {
  page: number
  totalPages: number
  onPage: (page: number) => void
  summary?: string
}) {
  if (totalPages <= 1 && !summary)
    return null
  return (
    <nav className="pagination" aria-label="分页">
      {summary && <p className="pagination-summary text-ui-caption">{summary}</p>}
      {totalPages > 1 && (
        <div className="pagination-pages">
          <StepButton label="上一页" enabled={page > 1} onClick={() => onPage(page - 1)}>
            <ChevronLeft size={18} aria-hidden="true" />
          </StepButton>
          {pageItems(page, totalPages).map((item, index, items) => item === 'gap'
            ? <span className="pagination-gap" aria-hidden="true" key={`gap-after-${items[index - 1]}`}>…</span>
            : (
                <button
                  type="button"
                  key={item}
                  aria-label={`第 ${item} 页`}
                  aria-current={item === page ? 'page' : undefined}
                  onClick={() => {
                    if (item !== page)
                      onPage(item)
                  }}
                >
                  {item}
                </button>
              ))}
          <StepButton label="下一页" enabled={page < totalPages} onClick={() => onPage(page + 1)}>
            <ChevronRight size={18} aria-hidden="true" />
          </StepButton>
        </div>
      )}
    </nav>
  )
}
