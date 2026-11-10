// 返回这次按键是否属于输入法，比如用回车确认候选词、用 Esc 取消拼写。这样的按键不是给输入框的。
// Safari 先结束组字再触发这次 keydown，这时 isComposing 已经是 false，只能靠 keyCode 229 认出来。
export function isImeKey(event: Pick<KeyboardEvent, 'isComposing' | 'keyCode'>): boolean {
  return event.isComposing || event.keyCode === 229
}
