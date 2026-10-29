import type { ReactNode, RefObject } from 'react'
import { Component, createRef } from 'react'
import { EmptyState } from '@/shared/ui'

interface ErrorBoundaryProps {
  children: ReactNode
  // fallback 为函数时，须把 retryButton 挂到"重试"按钮上：出错时若焦点在区域内，边界通过它把焦点移到按钮上。
  fallback?: ReactNode | ((retry: () => void, retryButton: RefObject<HTMLButtonElement | null>) => ReactNode)
  resetOn?: EventTarget
  // 只在值变化时重试一次。resetOn 在 Store 每次变化时都会重试，Store 变化频繁时，持续出错的区域会反复失败。
  resetKey?: number
  onError?: () => void
  // 仅当 fallback 为 null 且出错时焦点在区域内，才向屏幕阅读器播报。
  announce?: string
}

// 出错状态保存在实例上。主内容和面板内容的边界位于以页面或视图为 key 的元素内，切换时随之重新挂载，出错状态即被清除。
export class ErrorBoundary extends Component<ErrorBoundaryProps, { failed: boolean }, Element | null> {
  state = { failed: false }
  retryButton = createRef<HTMLButtonElement>()
  announcement = createRef<HTMLParagraphElement>()

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidMount() {
    this.props.resetOn?.addEventListener('change', this.reset)
  }

  // 出错的内容卸载时，其中的焦点会落到 body 上：卸载前先记下当前焦点。
  getSnapshotBeforeUpdate() {
    return document.activeElement
  }

  componentDidUpdate(previous: ErrorBoundaryProps, previousState: { failed: boolean }, focused: Element | null) {
    const { resetOn, resetKey, announce } = this.props
    if (previous.resetOn !== resetOn) {
      previous.resetOn?.removeEventListener('change', this.reset)
      resetOn?.addEventListener('change', this.reset)
    }
    if (previousState.failed && previous.resetKey !== resetKey)
      this.reset()
    const focusLost = this.state.failed && !previousState.failed && focused !== null && !focused.isConnected && document.activeElement === document.body
    if (!focusLost)
      return
    this.retryButton.current?.focus()
    if (this.announcement.current && announce)
      this.announcement.current.textContent = announce
  }

  componentDidCatch() {
    this.props.onError?.()
  }

  componentWillUnmount() {
    this.props.resetOn?.removeEventListener('change', this.reset)
  }

  retry = () => {
    this.setState({ failed: false })
  }

  reset = () => {
    if (this.state.failed)
      this.retry()
  }

  render() {
    if (!this.state.failed)
      return this.props.children
    const { fallback, announce } = this.props
    if (fallback === undefined) {
      return (
        <div className="page-content">
          <EmptyState title="这部分内容出错了" action={<button type="button" className="button-primary" ref={this.retryButton} onClick={this.retry}>重试</button>}>
            再试一次，或者先看看别的内容。播放不受影响。
          </EmptyState>
        </div>
      )
    }
    if (fallback === null)
      return announce ? <p className="sr-only" role="status" ref={this.announcement} /> : null
    return typeof fallback === 'function' ? fallback(this.retry, this.retryButton) : fallback
  }
}

export function RegionFailed({ message, retry, retryButton }: { message: string, retry: () => void, retryButton: RefObject<HTMLButtonElement | null> }) {
  return (
    <>
      <p className="text-ui-caption muted" role="status">{message}</p>
      <button type="button" className="button-secondary" ref={retryButton} onClick={retry}>重试</button>
    </>
  )
}
