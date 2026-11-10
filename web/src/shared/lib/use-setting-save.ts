import { useEffect, useReducer, useRef, useState } from 'react'
import { idleSave, settingSave, shownValue } from './setting-save'

// “已保存”显示 1.5 秒，再用 0.4 秒淡出，和 ui.css 里 .save-status-saved 的动画一致。
export const savedNoticeMs = 1900

// 改完立即保存。save 是同步函数时，写的是这台设备上的 store，抛出的是浏览器的存储错误。save 返回 Promise 时写的是服务端，失败时抛出 Error，message 是给用户看的原因。
export function useSettingSave<T>(saved: T, save: (value: T) => void | Promise<void>) {
  const [state, dispatch] = useReducer(settingSave<T>, idleSave)
  const [savedVersion, setSavedVersion] = useState(0)
  // state 要到下一次渲染才更新，连续点击时由这个 ref 阻止同时保存两次。
  const savingRef = useRef(false)

  useEffect(() => {
    if (state.status !== 'saved')
      return
    const timer = setTimeout(dispatch, savedNoticeMs, { type: 'expired' })
    return () => clearTimeout(timer)
  }, [state])

  const change = (value: T) => {
    if (savingRef.current || value === shownValue(state, saved))
      return
    dispatch({ type: 'start', value })
    let result: void | Promise<void>
    try {
      result = save(value)
    }
    catch {
      dispatch({ type: 'failed', error: '没有保存，浏览器不允许在这台设备上保存设置。' })
      return
    }
    const succeeded = () => {
      dispatch({ type: 'succeeded' })
      setSavedVersion(version => version + 1)
    }
    if (!(result instanceof Promise)) {
      succeeded()
      return
    }
    savingRef.current = true
    result.then(
      succeeded,
      (error: unknown) => dispatch({ type: 'failed', error: error instanceof Error && error.message ? error.message : '没有保存，再试一次。' }),
    ).finally(() => {
      savingRef.current = false
    })
  }

  return { value: shownValue(state, saved), state, savedVersion, change }
}
