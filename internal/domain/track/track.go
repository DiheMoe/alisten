package track

import (
	"strings"
	"time"
)

type Source string

const (
	NetEase  Source = "netease"
	QQ       Source = "qq"
	Link     Source = "link"
	Bilibili Source = "bilibili"
)

type Track struct {
	Source Source
	ID     string
}

// LinkID 是链接作为曲目 ID 的写法，对象存储中的对象键也由它得出：只把 scheme 和主机名中的 ASCII 字母转成小写，
// 其余字节一律不变（片段、查询参数的顺序、端口和百分号编码都可能有意义）。非 ASCII 字符不转小写：国际化域名的大小写映射
// 与 Unicode 的不同。link 必须是经过协议校验的链接。
func LinkID(link string) string {
	scheme, rest, _ := strings.Cut(link, "://")
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	return asciiLower(scheme) + "://" + asciiLower(rest[:end]) + rest[end:]
}

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

type Meta struct {
	Title    string
	Artist   string
	Album    string
	CoverURL string
	// 链接曲目的封面在对象存储中：只存对象键，对外的地址由 serve 拼接，公开地址改变之后，已保存的曲目和条目仍然有效。
	CoverKey ObjectKey
	PageURL  string
	Duration time.Duration
}

// 对象键不是地址：拼上对象存储的公开地址之后才能交给浏览器。
type ObjectKey string

type Prepared struct {
	Duration time.Duration
	Gain     float64
	Lyric    string
}
