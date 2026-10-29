import type { ErrorCode } from '@/core/realtime'
import { ApiError } from '@/core/api'
import { RequestError } from '@/core/realtime'

export type ErrorTexts = Partial<Record<ErrorCode, string | ((message: string) => string)>>

// 这两个错误码的 message 写给接口调用方，带字段路径或协议版本，界面上换成用户能照着做的说法；其余错误码的 message 本身就是给用户看的。
const texts: ErrorTexts = {
  INVALID_ARGUMENT: '提交的内容不符合要求，请检查后再试。',
  CLIENT_OUTDATED: '应用已经更新，稍后会自动刷新页面。',
}

export function errorText(code: ErrorCode, message: string, overrides: ErrorTexts = {}): string {
  const text = overrides[code] ?? texts[code]
  return typeof text === 'function' ? text(message) : text ?? message
}

// 没有错误码的 ApiError 和 RequestError 由前端构造，message 本来就是给用户看的；其他错误来自浏览器或程序缺陷，它们的 message 不给用户看。
export function errorMessage(error: unknown, overrides?: ErrorTexts): string {
  if (!(error instanceof ApiError || error instanceof RequestError))
    return '出错了，请稍后再试。'
  return error.code ? errorText(error.code, error.message, overrides) : error.message
}
