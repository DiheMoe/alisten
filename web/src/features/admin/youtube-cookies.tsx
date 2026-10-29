import type { YoutubeCredentialState } from './youtube-cookies-view'
import { useEffect, useId, useState } from 'react'
import { api } from '@/core/api'
import { formatDateTime } from '@/shared/lib/time'
import { Modal, notificationStore } from '@/shared/ui'
import { adminStore } from './admin-store'
import { ConfirmDialog } from './confirm-dialog'
import { canSubmitYoutubeCookies, shouldWarnAboutPlaintext, youtubeCookiesText, youtubeCookiesTooLarge, youtubeCookiesView } from './youtube-cookies-view'

function YouTubeCookiesDialog({ onSaved, onClose }: { onSaved: () => void, onClose: () => void }) {
  const [cookies, setCookies] = useState('')
  const [busy, setBusy] = useState(false)
  const tooLarge = youtubeCookiesTooLarge(cookies)
  const tooLargeId = useId()
  const save = async () => {
    if (!canSubmitYoutubeCookies(cookies, busy))
      return
    setBusy(true)
    try {
      // 空白和超过 64 KiB 的内容不会提交，INVALID_ARGUMENT 只剩认不出格式，这时的 message 是写给用户的。
      const saved = await adminStore.call(() => api.adminSetYoutubeCredential({ cookies }), { INVALID_ARGUMENT: message => message })
      if (saved !== null)
        onSaved()
    }
    finally {
      setBusy(false)
    }
  }
  return (
    <Modal title="填写 YouTube cookies" onClose={onClose}>
      <form
        className="form-stack"
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        {shouldWarnAboutPlaintext(window.location) && <p className="error-text text-ui-sm" role="alert">{youtubeCookiesText.plaintextWarning}</p>}
        <ol className="admin-steps text-ui-sm">
          <li>打开浏览器的无痕窗口，登录用来点歌的 YouTube 账号。账号可能被 YouTube 封禁，建议用不常用的账号。</li>
          <li>登录之后，在同一个标签页里打开 www.youtube.com/robots.txt，无痕窗口里只留这一个标签页。</li>
          <li>
            用下面任一种方式取得 cookies，粘贴到输入框：
            <ul>
              <li>用 Get cookies.txt LOCALLY 这类扩展导出当前网站（youtube.com）的 cookies.txt。无痕窗口里看不到扩展时，到扩展管理页打开“在无痕模式下启用”（Firefox 是“在隐私窗口中运行”）。</li>
              <li>按 F12（macOS 按 ⌥⌘I）打开开发者工具，在“网络”面板里刷新页面，点开 robots.txt 请求，复制“请求标头”（Firefox 是“请求头”）里 cookie 的值。</li>
            </ul>
          </li>
          <li>保存之后直接关掉这个无痕窗口，不要再在里面打开 YouTube 的页面：页面开着时 YouTube 会更换 cookies，刚保存的就会失效。</li>
        </ol>
        <p className="text-ui-sm muted">保存会覆盖已有 cookies；已保存的内容不会在此显示。</p>
        <div className="admin-field">
          <label>
            YouTube cookies
            <textarea data-autofocus value={cookies} onChange={event => setCookies(event.target.value)} required rows={12} spellCheck={false} aria-invalid={tooLarge} aria-describedby={tooLarge ? tooLargeId : undefined} placeholder="粘贴 cookies.txt 的内容，或请求头里 cookie 的值" />
          </label>
          {tooLarge && <p id={tooLargeId} className="error-text text-ui-sm" role="alert">{youtubeCookiesText.tooLarge}</p>}
        </div>
        <div className="dialog-actions">
          <button type="button" className="button-secondary" onClick={onClose}>取消</button>
          <button type="submit" className="button-primary" disabled={!canSubmitYoutubeCookies(cookies, busy)}>{busy ? '正在保存…' : '保存'}</button>
        </div>
      </form>
    </Modal>
  )
}

export function YouTubeCookies() {
  const [credential, setCredential] = useState<YoutubeCredentialState>()
  const [attempt, setAttempt] = useState(0)
  const [editing, setEditing] = useState(false)
  const [clearing, setClearing] = useState(false)
  useEffect(() => {
    let active = true
    void adminStore.call(() => api.adminGetYoutubeCredential()).then((result) => {
      if (active)
        setCredential(result)
    })
    return () => {
      active = false
    }
  }, [attempt])
  const view = youtubeCookiesView(credential)
  const refetch = () => {
    setCredential(undefined)
    setAttempt(n => n + 1)
  }
  return (
    <li className="admin-row">
      <div className="admin-row-copy">
        <strong className="text-ui-base">YouTube</strong>
        <span className="text-ui-sm muted" role="status">{view.status}</span>
        {view.updatedAtMs !== null && <span className="text-ui-sm muted">{`最近填写于 ${formatDateTime(view.updatedAtMs)}`}</span>}
        {shouldWarnAboutPlaintext(window.location) && <span className="error-text text-ui-sm" role="alert">{youtubeCookiesText.plaintextWarning}</span>}
      </div>
      <div className="admin-row-actions">
        <button type="button" className="button-secondary" disabled={credential === undefined} onClick={() => setEditing(true)}>{credential?.set ? '覆盖 cookies' : '填写 cookies'}</button>
        {credential?.set && <button type="button" className="button-secondary" onClick={() => setClearing(true)}>清除</button>}
      </div>
      {editing && (
        <YouTubeCookiesDialog
          onSaved={() => {
            setEditing(false)
            refetch()
            notificationStore.success('已保存 YouTube cookies')
          }}
          onClose={() => setEditing(false)}
        />
      )}
      {clearing && (
        <ConfirmDialog
          title="清除 YouTube cookies"
          confirmLabel="清除"
          onConfirm={async () => {
            if (await adminStore.call(() => api.adminDeleteYoutubeCredential()) === null)
              return
            setClearing(false)
            refetch()
            notificationStore.success('已清除 YouTube cookies')
          }}
          onClose={() => setClearing(false)}
        >
          清除后，下载 YouTube 链接时不再携带 cookies。
        </ConfirmDialog>
      )}
    </li>
  )
}
