import { EmptyState } from './empty-state'

// "重新加载"只在原地重新获取本页面的文件，应用的其余部分和尚未发送的输入都会保留，因此始终是主操作。
// 原地重试仍然失败，很可能是应用更新后旧文件已被删除，只有整页刷新才能取到新文件。
export function PageLoadFailed({ onRetry, failedAgain }: { onRetry: () => void, failedAgain: boolean }) {
  return (
    <div className="page-content">
      <EmptyState
        title="页面没有加载出来"
        action={(
          <>
            <button type="button" className="button-primary" onClick={onRetry}>重新加载</button>
            {failedAgain && (
              <>
                <p className="text-ui-caption muted">应用可能已经更新。刷新页面会丢掉还没发送的内容。</p>
                <button type="button" className="button-secondary" onClick={() => window.location.reload()}>刷新页面</button>
              </>
            )}
          </>
        )}
      >
        可能是网络中断，或者应用已经更新。
      </EmptyState>
    </div>
  )
}
