package track

import "testing"

func TestParseBilibiliID(t *testing.T) {
	type parsed struct {
		bvid string
		page int
	}
	valid := map[string]parsed{
		"BV1xx411c7mD":      {"BV1xx411c7mD", 1},
		"BV1xx411c7mD/2":    {"BV1xx411c7mD", 2},
		"BV1xx411c7mD/10":   {"BV1xx411c7mD", 10},
		"BV1xx411c7mD/9999": {"BV1xx411c7mD", 9999},
	}
	for id, want := range valid {
		bvid, page, ok := ParseBilibiliID(id)
		if !ok || bvid != want.bvid || page != want.page {
			t.Errorf("ParseBilibiliID(%q) = %q, %d, %v，期望 %q, %d, true", id, bvid, page, ok, want.bvid, want.page)
		}
	}
	for _, id := range []string{
		"", "av170001", "bv1xx411c7mD", "BV1xx411c7m", "BV1xx411c7mDD",
		"BV1xx411c7m0", "BV1xx411c7mO", "BV1xx411c7mI", "BV1xx411c7ml",
		"BV1xx411c7mD/", "BV1xx411c7mD/1", "BV1xx411c7mD/01", "BV1xx411c7mD/+2", "BV1xx411c7mD/10000",
		"BV1xx411c7mD/2/3", "BV1xx411c7mD?p=2", " BV1xx411c7mD",
	} {
		if bvid, page, ok := ParseBilibiliID(id); ok {
			t.Errorf("ParseBilibiliID(%q) = %q, %d, true，期望无效", id, bvid, page)
		}
	}
}

func TestBilibiliID(t *testing.T) {
	for _, page := range []int{1, 2, 37, 9999} {
		id := BilibiliID("BV1xx411c7mD", page)
		if bvid, got, ok := ParseBilibiliID(id); !ok || bvid != "BV1xx411c7mD" || got != page {
			t.Errorf("BilibiliID(第 %d P) = %q，解析回 %q, %d, %v", page, id, bvid, got, ok)
		}
	}
	if id := BilibiliID("BV1xx411c7mD", 1); id != "BV1xx411c7mD" {
		t.Errorf("第 1 P 的 ID = %q，期望不带序号", id)
	}
}

func TestBilibiliPageURL(t *testing.T) {
	tests := map[string]string{
		"BV1xx411c7mD":   "https://www.bilibili.com/video/BV1xx411c7mD",
		"BV1xx411c7mD/3": "https://www.bilibili.com/video/BV1xx411c7mD?p=3",
	}
	for id, want := range tests {
		if got := BilibiliPageURL(id); got != want {
			t.Errorf("BilibiliPageURL(%q) = %q，期望 %q", id, got, want)
		}
	}
}
