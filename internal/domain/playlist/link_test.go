package playlist

import (
	"testing"

	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestParseLink(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Link
		ok   bool
	}{
		{"网易云哈希路由", "https://music.163.com/#/playlist?id=123", Link{Source: track.NetEase, ID: "123", URL: "https://music.163.com/#/playlist?id=123"}, true},
		{"网易云桌面", "https://music.163.com/playlist?id=123", Link{Source: track.NetEase, ID: "123", URL: "https://music.163.com/playlist?id=123"}, true},
		{"网易云移动", "https://y.music.163.com/m/playlist?id=123&userid=9&creatorId=9", Link{Source: track.NetEase, ID: "123", URL: "https://y.music.163.com/m/playlist?id=123&userid=9&creatorId=9"}, true},
		{"网易云哈希带其他参数", "https://music.163.com/#/playlist?app_version=1&id=123", Link{Source: track.NetEase, ID: "123", URL: "https://music.163.com/#/playlist?app_version=1&id=123"}, true},
		{"网易云 http", "http://music.163.com/playlist?id=123", Link{Source: track.NetEase, ID: "123", URL: "http://music.163.com/playlist?id=123"}, true},
		{"QQ 桌面", "https://y.qq.com/n/ryqq/playlist/456", Link{Source: track.QQ, ID: "456", URL: "https://y.qq.com/n/ryqq/playlist/456"}, true},
		{"QQ 旧版", "https://y.qq.com/n/yqq/playlist/456.html", Link{Source: track.QQ, ID: "456", URL: "https://y.qq.com/n/yqq/playlist/456.html"}, true},
		{"QQ 新版网页", "https://y.qq.com/n/ryqq_v2/playlist/456?ADTAG=h5_share_playlist&mnst=1.18", Link{Source: track.QQ, ID: "456", URL: "https://y.qq.com/n/ryqq_v2/playlist/456?ADTAG=h5_share_playlist&mnst=1.18"}, true},
		{"QQ 移动 id", "https://i.y.qq.com/n2/m/share/details/taoge.html?id=456&hosteuin=x", Link{Source: track.QQ, ID: "456", URL: "https://i.y.qq.com/n2/m/share/details/taoge.html?id=456&hosteuin=x"}, true},
		{"QQ 移动 disstid", "https://i.y.qq.com/n2/m/share/details/taoge.html?disstid=456", Link{Source: track.QQ, ID: "456", URL: "https://i.y.qq.com/n2/m/share/details/taoge.html?disstid=456"}, true},
		{"QQ 新版分享页", "https://i2.y.qq.com/n3/other/pages/details/playlist.html?app_type=qmlite&&ADTAG=myqq&id=456&hosteuin=x", Link{Source: track.QQ, ID: "456", URL: "https://i2.y.qq.com/n3/other/pages/details/playlist.html?app_type=qmlite&&ADTAG=myqq&id=456&hosteuin=x"}, true},
		{"网易云短链接", "https://163cn.tv/abc", Link{Source: track.NetEase, Short: true, URL: "https://163cn.tv/abc"}, true},
		{"QQ 短链接", "https://c6.y.qq.com/base/fcgi-bin/u?__=abc", Link{Source: track.QQ, Short: true, URL: "https://c6.y.qq.com/base/fcgi-bin/u?__=abc"}, true},
		{"分享文本", "分享歌单：我的歌单 https://163cn.tv/abc (来自网易云音乐)", Link{Source: track.NetEase, Short: true, URL: "https://163cn.tv/abc"}, true},
		{"分享文本带句号", "看看这个 https://music.163.com/playlist?id=123。", Link{Source: track.NetEase, ID: "123", URL: "https://music.163.com/playlist?id=123"}, true},
		{"主机大小写", "HTTPS://Music.163.COM/playlist?id=123", Link{Source: track.NetEase, ID: "123", URL: "HTTPS://Music.163.COM/playlist?id=123"}, true},

		{"其他网站", "https://example.com/playlist?id=123", Link{}, false},
		{"主机后缀伪装", "https://music.163.com.evil.com/playlist?id=123", Link{}, false},
		{"userinfo 伪装", "https://music.163.com@evil.com/playlist?id=123", Link{}, false},
		{"网易云缺少 id", "https://music.163.com/playlist", Link{}, false},
		{"网易云 id 非数字", "https://music.163.com/playlist?id=abc", Link{}, false},
		{"网易云歌曲链接", "https://music.163.com/song?id=123", Link{}, false},
		{"QQ 缺少 id", "https://y.qq.com/n/ryqq/playlist/", Link{}, false},
		{"QQ id 非数字", "https://y.qq.com/n/ryqq/playlist/abc", Link{}, false},
		{"QQ 移动缺少 id", "https://i.y.qq.com/n2/m/share/details/taoge.html", Link{}, false},
		{"QQ 新版分享页缺少 id", "https://i2.y.qq.com/n3/other/pages/details/playlist.html?hosteuin=x", Link{}, false},
		{"QQ 分享域名上的其他页面", "https://i2.y.qq.com/n3/other/pages/details/song.html?id=456", Link{}, false},
		{"QQ 歌曲链接", "https://y.qq.com/n/ryqq/songDetail/abc", Link{}, false},
		{"没有链接", "只是一段文字", Link{}, false},
		{"空", "", Link{}, false},
		{"非 http", "ftp://music.163.com/playlist?id=123", Link{}, false},
		{"只取第一个链接", "https://example.com/a https://music.163.com/playlist?id=123", Link{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseLink(tt.raw)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ParseLink(%q) = %+v, %v; want %+v, %v", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestAllowedRedirectHost(t *testing.T) {
	for host, want := range map[string]bool{
		"163cn.tv": true, "music.163.com": true, "y.music.163.com": true,
		"c6.y.qq.com": true, "y.qq.com": true, "i.y.qq.com": true, "i2.y.qq.com": true, "Y.QQ.com": true,
		"evil.com": false, "music.163.com.evil.com": false, "qq.com": false, "": false, "127.0.0.1": false,
	} {
		if got := AllowedRedirectHost(host); got != want {
			t.Errorf("AllowedRedirectHost(%q) = %v; want %v", host, got, want)
		}
	}
}

func TestRecognized(t *testing.T) {
	for link, want := range map[string]bool{
		"https://music.163.com/#/playlist?id=123":  true,
		"https://y.qq.com/n/ryqq/playlist/7039876": true,
		"https://163cn.tv/abc":                     false,
		"https://example.com/playlist?id=1":        false,
	} {
		if got := Recognized(link); got != want {
			t.Errorf("Recognized(%q) = %v，期望 %v", link, got, want)
		}
	}
}
