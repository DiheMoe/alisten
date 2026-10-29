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
