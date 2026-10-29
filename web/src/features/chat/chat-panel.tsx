import type { Member } from '@/core/realtime'
import { MessageCircle, Send, Users, X } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { errorMessage } from '@/core/errors'
import { roomStore } from '@/core/room'
import { formatClock } from '@/shared/lib/time'
import { useStore } from '@/shared/lib/use-store'
import { Avatar, EmptyState, IconButton, notificationStore } from '@/shared/ui'
import { chatStore } from './chat-store'
import './chat.css'

const batchSize = 80
const pinnedDistancePx = 60
const noMembers: Member[] = []

function OnlineMembers() {
  const sessions = useStore(roomStore, s => s.room?.members.sessions ?? noMembers)
  const onlineCount = useStore(roomStore, s => s.room?.members.onlineCount ?? 0)
  const [open, setOpen] = useState(false)
  const anchorRef = useRef<HTMLDivElement>(null)
  const popoverId = useId()
  useEffect(() => {
    if (!open)
      return
    const dismiss = (event: PointerEvent) => {
      if (!anchorRef.current?.contains(event.target as Node))
        setOpen(false)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape')
        setOpen(false)
    }
    document.addEventListener('pointerdown', dismiss)
    document.addEventListener('keydown', escape)
    return () => {
      document.removeEventListener('pointerdown', dismiss)
      document.removeEventListener('keydown', escape)
    }
  }, [open])
  // 一个成员可以同时有多个会话，在线列表中每个会话占一项。
  const occurrences = new Map<string, number>()
  return (
    <div className="member-popover-anchor" ref={anchorRef}>
      <button
        type="button"
        className="button-ghost chat-members-toggle text-ui-sm"
        aria-label={`在线成员，${onlineCount} 人`}
        aria-controls={open ? popoverId : undefined}
        aria-expanded={open}
        aria-haspopup="dialog"
        onClick={() => setOpen(value => !value)}
      >
        <Users size={16} aria-hidden="true" />
        {`${onlineCount} 人`}
      </button>
      {open && (
        <div className="member-popover" id={popoverId} role="dialog" aria-label="在线成员">
          <div className="member-popover-head">
            <strong className="text-ui-sm">在线成员</strong>
            <span className="text-ui-xs muted">{`${onlineCount} 人`}</span>
          </div>
          <div className="member-popover-list">
            {sessions.map((member) => {
              const occurrence = (occurrences.get(member.id) ?? 0) + 1
              occurrences.set(member.id, occurrence)
              return (
                <div className="member text-ui-sm" key={`${member.id}:${occurrence}`}>
                  <Avatar hash={member.avatarHash} name={member.name} className="text-ui-xs" />
                  <span className="truncate">{member.name}</span>
                </div>
              )
            })}
            {!sessions.length && <p className="muted text-ui-sm">连上房间之后显示在线成员。</p>}
          </div>
        </div>
      )}
    </div>
  )
}

export function ChatPanel({ compact = false, onClose }: { compact?: boolean, onClose?: () => void }) {
  const items = useStore(chatStore, s => s.items)
  const selfId = useStore(roomStore, s => s.member?.id)
  const connected = useStore(roomStore, s => s.status === 'open')
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [visibleCount, setVisibleCount] = useState(batchSize)
  const [readCount, setReadCount] = useState(items.length)
  const [atBottom, setAtBottom] = useState(true)
  const listRef = useRef<HTMLDivElement>(null)
  const pinnedRef = useRef(true)
  // state 要到下一次渲染才更新，连续按回车时由这个 ref 阻止重复发送。
  const sendingRef = useRef(false)
  useEffect(() => {
    if (pinnedRef.current)
      listRef.current?.scrollTo({ top: listRef.current.scrollHeight })
  }, [items])
  const first = Math.max(0, items.length - visibleCount)
  const scrollToBottom = () => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight })
    pinnedRef.current = true
    setReadCount(chatStore.state.items.length)
    setAtBottom(true)
  }
  const send = async () => {
    const content = draft.trim()
    if (!connected || !content || sendingRef.current)
      return
    sendingRef.current = true
    setSending(true)
    try {
      await chatStore.send(content)
      // 请求期间输入框仍可编辑：只去掉已发出的部分，期间继续输入的内容要保留。
      setDraft(current => current.startsWith(draft) ? current.slice(draft.length) : current)
      scrollToBottom()
    }
    catch (error) {
      notificationStore.error(errorMessage(error))
    }
    finally {
      sendingRef.current = false
      setSending(false)
    }
  }
  return (
    <section className={`chat-panel${compact ? ' chat-compact' : ''}`} aria-label="房间聊天">
      <header className="chat-heading">
        <h2 className="text-ui-lg">房间聊天</h2>
        <div className="chat-heading-actions">
          <OnlineMembers />
          {onClose && <IconButton icon={X} label="关闭侧栏" onClick={onClose} />}
        </div>
      </header>
      <div
        className="chat-messages"
        ref={listRef}
        onScroll={(event) => {
          const list = event.currentTarget
          pinnedRef.current = list.scrollHeight - list.scrollTop - list.clientHeight < pinnedDistancePx
          setAtBottom(pinnedRef.current)
          if (pinnedRef.current)
            setReadCount(items.length)
        }}
      >
        {first > 0 && (
          <button type="button" className="button-ghost text-ui-sm" onClick={() => setVisibleCount(n => n + batchSize)}>
            查看更早的消息
          </button>
        )}
        {!items.length && (
          <EmptyState icon={MessageCircle} title="好音乐，一起分享">
            打个招呼，或分享这首歌带给你的心情。
          </EmptyState>
        )}
        {items.slice(first).map(item => item.kind === 'message'
          ? (
              <article className={`chat-message${item.member.id === selfId ? ' self' : ''}`} key={item.key}>
                <Avatar hash={item.member.avatarHash} name={item.member.name} className="text-ui-sm" />
                <div className="message-content">
                  <div className="message-meta text-ui-xs muted">
                    <span>{item.member.name}</span>
                    <time dateTime={new Date(item.atMs).toISOString()}>{formatClock(item.atMs)}</time>
                  </div>
                  <p className="message-bubble text-ui-base">{item.text}</p>
                </div>
              </article>
            )
          : (
              <p className="chat-activity text-ui-caption muted" key={item.key}>
                <span>{item.member ? `${item.member.name} ${item.text}` : item.text}</span>
                <time dateTime={new Date(item.atMs).toISOString()}>{formatClock(item.atMs)}</time>
              </p>
            ))}
      </div>
      {!atBottom && items.length > readCount && (
        <button type="button" className="new-messages text-ui-sm" onClick={scrollToBottom}>
          查看新消息
        </button>
      )}
      <form
        className="chat-composer"
        onSubmit={(event) => {
          event.preventDefault()
          void send()
        }}
      >
        <input
          type="text"
          aria-label="聊天消息"
          placeholder={connected ? '聊聊这首歌…' : '连上房间之后开始聊天'}
          value={draft}
          onChange={event => setDraft(event.target.value)}
          maxLength={500}
          disabled={!connected}
        />
        <IconButton icon={Send} label="发送消息" type="submit" className="chat-send" disabled={!connected || !draft.trim() || sending} />
      </form>
    </section>
  )
}
