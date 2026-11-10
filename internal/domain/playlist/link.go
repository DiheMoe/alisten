package playlist

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/dihemoe/alisten/internal/domain/track"
)

// URL 是从文本中取出的链接；短链接要靠它去解析。
type Link struct {
	Source track.Source
	ID     string
	Short  bool
	URL    string
}

var (
	urlPattern = regexp.MustCompile(`(?i)https?://[^\s<>"'，。；！？、）】》]+`)
	digits     = regexp.MustCompile(`^[0-9]+$`)
	qqPath     = regexp.MustCompile(`^/n/(?:ryqq|yqq)/playlist/([0-9]+)(?:\.html)?$`)
)

// 重定向只能落在这些主机上，按小写比较。
var redirectHosts = map[string]bool{
	"163cn.tv":        true,
	"music.163.com":   true,
	"y.music.163.com": true,
	"c6.y.qq.com":     true,
	"y.qq.com":        true,
	"i.y.qq.com":      true,
}

func AllowedRedirectHost(host string) bool {
	return redirectHosts[strings.ToLower(host)]
}

// 用户粘贴的分享文本里夹着别的文字，所以只取第一个 http(s) 链接来识别，后面的链接不看。
func ParseLink(raw string) (Link, bool) {
	link := strings.TrimRight(urlPattern.FindString(raw), ".,;:!?)]}>")
	if link == "" {
		return Link{}, false
	}
	u, err := url.Parse(link)
	if err != nil || u.User != nil || u.Port() != "" {
		return Link{}, false
	}

	switch strings.ToLower(u.Hostname()) {
	case "163cn.tv":
		return Link{Source: track.NetEase, Short: true, URL: link}, true
	case "c6.y.qq.com":
		return Link{Source: track.QQ, Short: true, URL: link}, true
	case "music.163.com", "y.music.163.com":
		path, query := u.Path, u.Query()
		// 桌面版把路由放在片段里：/#/playlist?id=<id>。
		if frag, ok := strings.CutPrefix(u.Fragment, "/"); ok && path == "/" {
			p, q, _ := strings.Cut(frag, "?")
			path = "/" + p
			if query, err = url.ParseQuery(q); err != nil {
				return Link{}, false
			}
		}
		if path != "/playlist" && path != "/m/playlist" {
			return Link{}, false
		}
		if id := query.Get("id"); digits.MatchString(id) {
			return Link{Source: track.NetEase, ID: id, URL: link}, true
		}
	case "y.qq.com":
		if m := qqPath.FindStringSubmatch(u.Path); m != nil {
			return Link{Source: track.QQ, ID: m[1], URL: link}, true
		}
	case "i.y.qq.com":
		if u.Path != "/n2/m/share/details/taoge.html" {
			return Link{}, false
		}
		q := u.Query()
		for _, key := range []string{"id", "disstid"} {
			if id := q.Get(key); digits.MatchString(id) {
				return Link{Source: track.QQ, ID: id, URL: link}, true
			}
		}
	}
	return Link{}, false
}

// 链接已经能直接识别出歌单，不必再跟随跳转。
func Recognized(link string) bool {
	l, ok := ParseLink(link)
	return ok && !l.Short
}
