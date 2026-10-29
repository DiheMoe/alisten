package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ArgumentError 的 Error() 原样作为 INVALID_ARGUMENT 的 message 返回给客户端。
type ArgumentError struct {
	Path   string
	Reason string
}

func (e *ArgumentError) Error() string {
	if e.Path == "" {
		return e.Reason
	}
	return e.Path + "：" + e.Reason
}

func invalid(path, reason string) error {
	return &ArgumentError{Path: path, Reason: reason}
}

func field(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

var null = []byte("null")

func decodeObject(raw json.RawMessage, path string, known ...string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, invalid(path, "应为 JSON 对象")
	}
	return m, rejectUnknown(m, path, known...)
}

func rejectUnknown(m map[string]json.RawMessage, path string, known ...string) error {
	// 按名称顺序检查，有多个未知字段时报告的总是同一个。
	for _, name := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(known, name) {
			return invalid(field(path, name), "未知字段")
		}
	}
	return nil
}

func requireFields(m map[string]json.RawMessage, path string, names ...string) error {
	for _, name := range names {
		if _, ok := m[name]; !ok {
			return invalid(field(path, name), "缺少必填字段")
		}
	}
	return nil
}

// encoding/json 把 null 解码为零值而不报错，所以 null 要单独拒绝。
func decodeString(raw json.RawMessage, path string) (string, error) {
	var s string
	if bytes.Equal(raw, null) || json.Unmarshal(raw, &s) != nil {
		return "", invalid(path, "应为字符串")
	}
	// PostgreSQL 的 text 无法存储 U+0000，含有它的文本一旦进入落盘批次就会让落盘失败。
	if strings.ContainsRune(s, 0) {
		return "", invalid(path, "不能包含 U+0000")
	}
	return s, nil
}

func decodeInteger(raw json.RawMessage, path string) (int64, error) {
	var n int64
	if bytes.Equal(raw, null) || json.Unmarshal(raw, &n) != nil {
		return 0, invalid(path, "应为整数")
	}
	return n, nil
}

func checkMinLength(s, path string, n int) error {
	if utf8.RuneCountInString(s) < n {
		return invalid(path, fmt.Sprintf("长度不能少于 %d 个字符", n))
	}
	return nil
}

func checkMaxLength(s, path string, n int) error {
	if utf8.RuneCountInString(s) > n {
		return invalid(path, fmt.Sprintf("长度不能超过 %d 个字符", n))
	}
	return nil
}

func checkMaxBytes(s, path string, n int) error {
	if len(s) > n {
		return invalid(path, fmt.Sprintf("不能超过 %d 字节", n))
	}
	return nil
}

func checkMinimum(v int64, path string, n int64) error {
	if v < n {
		return invalid(path, fmt.Sprintf("不能小于 %d", n))
	}
	return nil
}

func checkMaximum(v int64, path string, n int64) error {
	if v > n {
		return invalid(path, fmt.Sprintf("不能大于 %d", n))
	}
	return nil
}

func checkPattern(s, path string, re *regexp.Regexp) error {
	if !re.MatchString(s) {
		return invalid(path, "格式不正确")
	}
	return nil
}

func checkURL(s, path string) error {
	if !validURL(s) {
		return invalid(path, "应为 http 或 https 的链接")
	}
	return nil
}

var (
	bracketedHost = regexp.MustCompile(`^\[[0-9A-Fa-f:.]+\]$`)
	numericLabel  = regexp.MustCompile(`^(?i:[0-9]+|0x[0-9a-f]*)$`)
)

// x-url：scheme 为 http 或 https（不区分大小写）的绝对地址。浏览器的 new URL() 与 url.Parse 对一些写法的判断不同，
// 网页生成的校验函数按同样的规则检查，两边的结果才一致（protocol/testdata 中的用例由两边的测试共用）：
//   - 不含控制字符、空格和反斜杠（浏览器把 \ 当作 /），不含用户信息；
//   - 主机名只由字母、数字、-、.、_、~ 和非 ASCII 字符组成，或者是方括号中的 IPv6 地址（浏览器会解码主机名中的 % 编码）；
//   - 最后一段是数字（或以 0x 开头）的主机名会被浏览器按 IPv4 地址解析，所以必须是规范的 IPv4 地址；
//   - 端口不超过 65535，路径和片段中的 % 后面必须是两位十六进制数（url.Parse 不接受其他写法，浏览器却照样接受）。
//
// 国际化域名（主机名含非 ASCII 字符，或者有以 xn-- 开头的标签）只由浏览器检查：不合法时浏览器拒绝而这里接受，到准备时才会下载失败。
func validURL(s string) bool {
	if strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f || r == '\\' }) {
		return false
	}
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok || !strings.EqualFold(scheme, "http") && !strings.EqualFold(scheme, "https") {
		return false
	}
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	host, port := authority, ""
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']') + 1
		host = authority[:end]
		if end == 0 || !bracketedHost.MatchString(host) {
			return false
		}
		if after := authority[end:]; after != "" {
			if port, ok = strings.CutPrefix(after, ":"); !ok {
				return false
			}
		}
	} else {
		if h, p, ok := strings.Cut(authority, ":"); ok {
			host, port = h, p
		}
		if host == "" || strings.ContainsFunc(host, func(r rune) bool { return r < utf8.RuneSelf && !hostChar(r) }) {
			return false
		}
		labels := strings.Split(strings.TrimSuffix(host, "."), ".")
		if numericLabel.MatchString(labels[len(labels)-1]) {
			if a, err := netip.ParseAddr(host); err != nil || !a.Is4() {
				return false
			}
		}
	}
	if strings.Trim(port, "0123456789") != "" {
		return false
	}
	if n, err := strconv.Atoi(port); port != "" && (err != nil || n > 65535) {
		return false
	}
	path, fragment, _ := strings.Cut(tail, "#")
	path, _, _ = strings.Cut(path, "?")
	if !validEscapes(path) || !validEscapes(fragment) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Hostname() != ""
}

func hostChar(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune("-._~", r)
}

func validEscapes(s string) bool {
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			return true
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return false
		}
		s = s[i+3:]
	}
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// IDL 中 oneOf 的每个分支都用 additionalProperties: false 限定了字段，所以除去各分支共用的可选字段，出现的字段必须恰好是其中一组。
func checkOneOf(m map[string]json.RawMessage, path, reason string, optional []string, groups ...[]string) error {
	n := len(m)
	for _, name := range optional {
		if _, ok := m[name]; ok {
			n--
		}
	}
	for _, group := range groups {
		if n == len(group) && requireFields(m, path, group...) == nil {
			return nil
		}
	}
	return invalid(path, reason)
}
