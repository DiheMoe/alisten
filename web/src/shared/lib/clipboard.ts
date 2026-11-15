// 写不进剪贴板时返回 false，由调用方让用户手动复制。非安全上下文（比如局域网的明文 HTTP）没有剪贴板接口，用户也可能拒绝授权。
// 有的浏览器在请求返回之后已经不算用户操作，也会拒绝写入。
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  }
  catch {
    return false
  }
}
