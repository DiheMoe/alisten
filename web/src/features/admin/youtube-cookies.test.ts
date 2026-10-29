import type { AdminGetYoutubeCredentialResponse } from '@/core/api'
import { describe, expect, it } from 'vitest'
import { canSubmitYoutubeCookies, shouldWarnAboutPlaintext, youtubeCookiesText, youtubeCookiesTooLarge, youtubeCookiesView } from './youtube-cookies-view'

describe('canSubmitYoutubeCookies', () => {
  it('空字符串和纯空白不能提交', () => {
    expect(canSubmitYoutubeCookies('', false)).toBe(false)
    expect(canSubmitYoutubeCookies(' \n\t ', false)).toBe(false)
  })

  it('非空内容可以提交，保存期间不能重复提交', () => {
    expect(canSubmitYoutubeCookies('# Netscape HTTP Cookie File\n', false)).toBe(true)
    expect(canSubmitYoutubeCookies('# Netscape HTTP Cookie File\n', true)).toBe(false)
  })

  it('超过 64 KiB 不能提交', () => {
    expect(canSubmitYoutubeCookies('a'.repeat(64 * 1024), false)).toBe(false)
  })
})

describe('youtubeCookiesTooLarge', () => {
  // 请求体 {"cookies":"…"} 在内容之外还有 14 个字节。
  it('按请求体的字节数计算，刚好 64 KiB 不算超过', () => {
    expect(youtubeCookiesTooLarge('a'.repeat(64 * 1024 - 14))).toBe(false)
    expect(youtubeCookiesTooLarge('a'.repeat(64 * 1024 - 13))).toBe(true)
  })

  it('汉字按 UTF-8 的字节数计算，换行和制表符按 JSON 转义之后计算', () => {
    expect(youtubeCookiesTooLarge('中'.repeat(21_840))).toBe(false)
    expect(youtubeCookiesTooLarge('中'.repeat(21_841))).toBe(true)
    expect(youtubeCookiesTooLarge('\t'.repeat(32_761))).toBe(false)
    expect(youtubeCookiesTooLarge('\t'.repeat(32_762))).toBe(true)
    expect(youtubeCookiesTooLarge('\n'.repeat(32_761))).toBe(false)
    expect(youtubeCookiesTooLarge('\n'.repeat(32_762))).toBe(true)
  })
})

describe('shouldWarnAboutPlaintext', () => {
  const cases = [
    { location: { protocol: 'https:', hostname: 'music.example.com' }, want: false },
    { location: { protocol: 'http:', hostname: 'localhost' }, want: false },
    { location: { protocol: 'http:', hostname: '127.0.0.1' }, want: false },
    { location: { protocol: 'http:', hostname: '127.0.0.2' }, want: false },
    { location: { protocol: 'http:', hostname: 'app.localhost' }, want: false },
    { location: { protocol: 'http:', hostname: '::1' }, want: false },
    { location: { protocol: 'http:', hostname: '[::1]' }, want: false },
    { location: { protocol: 'http:', hostname: '192.168.1.20' }, want: true },
    { location: { protocol: 'http:', hostname: 'music.example.com' }, want: true },
  ]

  for (const tc of cases) {
    it(`${tc.location.protocol}//${tc.location.hostname} 的警示为 ${tc.want}`, () => {
      expect(shouldWarnAboutPlaintext(tc.location)).toBe(tc.want)
    })
  }
})

describe('youtubeCookiesText', () => {
  it('说明明文传输风险', () => {
    expect(youtubeCookiesText.plaintextWarning).toBe('当前连接不是 HTTPS，填写的 cookies 会以明文在网络上传输，只在可信网络中填写。')
  })
})

describe('youtubeCookiesView', () => {
  const cases: { name: string, state: AdminGetYoutubeCredentialResponse | null | undefined, want: ReturnType<typeof youtubeCookiesView> }[] = [
    { name: '正在查询', state: undefined, want: { status: '正在查询…', updatedAtMs: null } },
    { name: '查询失败', state: null, want: { status: '查询失败', updatedAtMs: null } },
    { name: '未配置', state: { set: false, updatedAtMs: 0 }, want: { status: '未配置', updatedAtMs: null } },
    { name: '已配置并显示更新时间', state: { set: true, updatedAtMs: 1_793_184_000_000 }, want: { status: '已配置', updatedAtMs: 1_793_184_000_000 } },
  ]

  for (const tc of cases) {
    it(tc.name, () => {
      expect(youtubeCookiesView(tc.state)).toEqual(tc.want)
    })
  }

  it('已有 cookies 不进入状态视图', () => {
    const state = { set: true, updatedAtMs: 1_793_184_000_000, cookies: '虚构 cookie，不能回显' }
    expect(youtubeCookiesView(state)).toEqual({ status: '已配置', updatedAtMs: 1_793_184_000_000 })
  })
})
