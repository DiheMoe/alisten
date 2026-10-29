package bilibili

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

func TestRecognizeVideoLinks(t *testing.T) {
	c, tr, _, _ := serve(t, nil)
	links := map[string]string{
		"BV1xx411c7mD":   "BV1xx411c7mD",
		"BV1xx411c7mD/2": "BV1xx411c7mD/2",
		"av170001":       "BV17x411w7KC",
		"https://www.bilibili.com/video/BV1xx411c7mD":                   "BV1xx411c7mD",
		"https://www.bilibili.com/video/BV1xx411c7mD/":                  "BV1xx411c7mD",
		"https://www.bilibili.com/video/BV1xx411c7mD?p=1":               "BV1xx411c7mD",
		"https://www.bilibili.com/video/BV1xx411c7mD?p=2":               "BV1xx411c7mD/2",
		"https://www.bilibili.com/video/BV1xx411c7mD/?p=12&vd_source=x": "BV1xx411c7mD/12",
		"https://m.bilibili.com/video/BV1xx411c7mD?p=3":                 "BV1xx411c7mD/3",
		"https://www.bilibili.com/video/av170001":                       "BV17x411w7KC",
		"https://www.bilibili.com/video/av170001?p=2":                   "BV17x411w7KC/2",
	}
	for link, want := range links {
		id, ok, err := c.Recognize(t.Context(), link)
		if err != nil || !ok || id != want {
			t.Errorf("Recognize(%q) = %q, %v, %v，期望 %q, true, nil", link, id, ok, err, want)
		}
	}
	if len(tr.requests()) != 0 {
		t.Fatalf("直接识别不应请求平台，请求了 %+v", tr.requests())
	}
}

func TestRecognizeNonBilibili(t *testing.T) {
	c, _, _, _ := serve(t, nil)
	for _, link := range []string{
		"https://example.com/video/BV1xx411c7mD",
		"https://www.bilibili.com.evil.example/video/BV1xx411c7mD",
		"https://b23.tv.evil.example/abc",
		"https://www.youtube.com/watch?v=x",
		"hello world",
	} {
		if id, ok, err := c.Recognize(t.Context(), link); err != nil || ok || id != "" {
			t.Errorf("Recognize(%q) = %q, %v, %v，期望 \"\", false, nil", link, id, ok, err)
		}
	}
}

func TestRecognizeNonVideo(t *testing.T) {
	c, _, _, _ := serve(t, nil)
	for _, link := range []string{
		"https://www.bilibili.com/bangumi/play/ep733316",
		"https://www.bilibili.com/bangumi/play/ss45929",
		"https://live.bilibili.com/22603245",
		"https://space.bilibili.com/208259",
		"https://www.bilibili.com/",
		"https://www.bilibili.com/video/BV123",
		"https://www.bilibili.com/video/BV1xx411c7mD?p=10000",
	} {
		_, _, err := c.Recognize(t.Context(), link)
		var rejected *port.RejectedError
		if !errors.As(err, &rejected) {
			t.Errorf("Recognize(%q) = %v，期望 RejectedError", link, err)
		}
	}
}

func TestRecognizeShortLink(t *testing.T) {
	c, tr, obs, _ := serve(t, map[string]http.HandlerFunc{
		"/x7aBc": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://www.bilibili.com/video/BV1xx411c7mD?p=2")
			w.WriteHeader(http.StatusFound)
		},
	})
	id, ok, err := c.Recognize(t.Context(), "https://b23.tv/x7aBc")
	if err != nil || !ok || id != "BV1xx411c7mD/2" {
		t.Fatalf("短链 = %q, %v, %v", id, ok, err)
	}
	reqs := tr.requests()
	if len(reqs) != 1 || reqs[0].host != "b23.tv" {
		t.Fatalf("请求 = %+v", reqs)
	}
	obs.check(t, "bilibili:recognize:ok")
}

func TestRecognizeShortLinkToNonVideo(t *testing.T) {
	c, _, _, _ := serve(t, map[string]http.HandlerFunc{
		"/x7aBc": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://live.bilibili.com/22603245")
			w.WriteHeader(http.StatusFound)
		},
	})
	_, _, err := c.Recognize(t.Context(), "https://b23.tv/x7aBc")
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("指向直播的短链 = %v，期望 RejectedError", err)
	}
}

func TestRecognizeShortLinkFailure(t *testing.T) {
	c, _, obs, _ := serve(t, map[string]http.HandlerFunc{
		"/x7aBc": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	_, _, err := c.Recognize(t.Context(), "https://b23.tv/x7aBc")
	var rejected *port.RejectedError
	if err == nil || errors.As(err, &rejected) {
		t.Fatalf("短链请求失败 = %v，期望普通错误", err)
	}
	if strings.Contains(err.Error(), "x7aBc") {
		t.Fatalf("错误包含短链地址：%v", err)
	}
	obs.check(t, "bilibili:recognize:error")
}
