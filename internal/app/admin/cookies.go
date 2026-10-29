package admin

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// 用户粘贴的内容无法识别为 cookies 时返回，文本直接展示给后台用户。
var ErrUnrecognizedCookies = errors.New("不是能识别的 cookies：请粘贴扩展导出的 cookies.txt，或开发者工具里请求头 cookie 的值。")

// normalizeCookies 把用户粘贴的内容统一成 Netscape 格式：扩展导出的 cookies.txt 原样保存
// （换行统一成 \n），开发者工具里复制的 Cookie 请求头逐条转换。数据库里只存 Netscape 格式。
func normalizeCookies(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", ErrUnrecognizedCookies
	}
	if netscape, ok := normalizeNetscape(s); ok {
		return netscape, nil
	}
	if header, ok := convertCookieHeader(s); ok {
		return header, nil
	}
	return "", ErrUnrecognizedCookies
}

// 空行和注释行原样保留但不校验；#HttpOnly_ 开头的是有效 cookie 行，不是注释。
// 其余每行必须是制表符分隔的 7 列，且至少有一行。
func normalizeNetscape(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	valid := 0
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		lines[i] = line
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#HttpOnly_") {
			continue
		}
		if len(strings.Split(line, "\t")) != 7 {
			return "", false
		}
		valid++
	}
	if valid == 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

// Cookie 请求头只有一行：可以带不区分大小写的 Cookie: 前缀，内容是用 ; 分隔的 名字=值。
// 统一转换成 .youtube.com 的 Secure cookie，一年后过期。
func convertCookieHeader(s string) (string, bool) {
	if strings.ContainsAny(s, "\n\t") {
		return "", false
	}
	body := s
	if len(body) >= len("cookie:") && strings.EqualFold(body[:len("cookie:")], "cookie:") {
		body = strings.TrimSpace(body[len("cookie:"):])
	}
	expiry := strconv.FormatInt(time.Now().AddDate(1, 0, 0).Unix(), 10)
	var lines []string
	for _, part := range strings.Split(body, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name == "" {
			return "", false
		}
		lines = append(lines, ".youtube.com\tTRUE\t/\tTRUE\t"+expiry+"\t"+name+"\t"+value)
	}
	if len(lines) == 0 {
		return "", false
	}
	return "# Netscape HTTP Cookie File\n" + strings.Join(lines, "\n"), true
}
