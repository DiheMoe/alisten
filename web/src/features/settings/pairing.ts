import { PairingCodePattern } from '@/core/realtime'

// 手动输入时常带空格、连字符或小写字母，片段里的配对码也可能被转义过。
export function normalizePairingCode(input: string): string {
  let text = input
  try {
    text = decodeURIComponent(input)
  }
  catch {}
  return text.toUpperCase().replace(/[\s-]/g, '')
}

export function isPairingCode(code: string): boolean {
  return PairingCodePattern.test(code)
}

// 分成两组显示，照着输入时不容易看串。
export function formatPairingCode(code: string): string {
  return `${code.slice(0, 4)}-${code.slice(4)}`
}

export function secondsLeft(expiresAtMs: number, nowMs: number): number {
  return Math.max(0, Math.ceil((expiresAtMs - nowMs) / 1000))
}

export function countdownText(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}
