package track

import "testing"

func TestLinkID(t *testing.T) {
	tests := map[string]string{
		"HTTPS://WWW.YouTube.COM/watch?v=dQw4w9WgXcQ": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"Http://Example.com":                          "http://example.com",
		"HTTPS://Example.com:8443/A/B?Q=1&a=2#Frag":   "https://example.com:8443/A/B?Q=1&a=2#Frag",
		"HTTPS://Example.com?Q=A":                     "https://example.com?Q=A",
		"HTTPS://Example.com#Frag":                    "https://example.com#Frag",
		"HTTPS://Example.com/%E4%BD%A0?x=%2F":         "https://example.com/%E4%BD%A0?x=%2F",
		"HTTPS://BÜCHER.Example/A":                    "https://bÜcher.example/A",
	}
	for link, want := range tests {
		if got := LinkID(link); got != want {
			t.Errorf("LinkID(%q) = %q，期望 %q", link, got, want)
		}
	}
}

func TestPlatformPageURL(t *testing.T) {
	tests := []struct{ got, want string }{
		{NetEasePageURL("186016"), "https://music.163.com/#/song?id=186016"},
		{QQPageURL("0039MnYb0qxYhV"), "https://y.qq.com/n/ryqq/songDetail/0039MnYb0qxYhV"},
		{NetEasePageURL("1&id=2#x"), "https://music.163.com/#/song?id=1%26id%3D2%23x"},
		{QQPageURL("a/../b?c"), "https://y.qq.com/n/ryqq/songDetail/a%2F..%2Fb%3Fc"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("页面链接 = %q，期望 %q", tt.got, tt.want)
		}
	}
}
