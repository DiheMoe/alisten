import { api } from '@/core/api'

// 按用户名保存服务端发给本机的登录设备凭证，因为一台设备可能登录过多个账号。退出登录时保留凭证，下次登录这个账号时带上，
// 这样别人反复猜这个账号的密码时，本机仍然能登录。
const devicesKey = 'alisten.login-devices'
// 与协议中的 LoginDevice 一致。本机保存的内容被改坏时不能带上，否则服务端会拒绝整个登录请求。
const devicePattern = /^[\w-]{43}$/

function loadDevices(): Record<string, unknown> {
  try {
    const devices: unknown = JSON.parse(localStorage.getItem(devicesKey) ?? '{}')
    if (typeof devices === 'object' && devices !== null)
      return devices as Record<string, unknown>
  }
  catch {}
  return {}
}

// 返回账号的新成员 token。
export async function loginWithPassword(username: string, password: string): Promise<string> {
  const device = loadDevices()[username]
  const response = await api.login(typeof device === 'string' && devicePattern.test(device) ? { username, password, device } : { username, password })
  // 等待响应期间别的标签页可能保存了别的账号的凭证，重新读取之后再写回。
  localStorage.setItem(devicesKey, JSON.stringify({ ...loadDevices(), [username]: response.device }))
  return response.memberToken
}
