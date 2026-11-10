package porttest

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func mediaKeys(items []port.StoredMedia) []string {
	keys := make([]string, len(items))
	for i, m := range items {
		keys[i] = m.Key
	}
	return keys
}

// 与 postgres 适配器的 TestStoredMediaPages 相同的规则。
func TestMediaLibraryPages(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	played := func(h time.Duration) *time.Time {
		at := t0.Add(h * time.Hour)
		return &at
	}
	lib := NewMediaLibrary(
		port.StoredMedia{Key: "a", Source: track.Link, Link: "https://example.com/c", Title: "Summer 50%_OFF", Size: 300, StoredAt: t0.Add(time.Hour)},
		port.StoredMedia{Key: "b", Source: track.Link, Link: "https://example.com/a", Title: "50abc_off", Size: 300, StoredAt: t0.Add(3 * time.Hour), LastPlayedAt: played(5)},
		port.StoredMedia{Key: "c", Source: track.Link, Link: "https://example.com/b", Title: "50%Xoff", Size: 100, StoredAt: t0.Add(2 * time.Hour), LastPlayedAt: played(4)},
		port.StoredMedia{Key: "d", Source: track.Bilibili, Link: "https://example.com/d", Title: "晴天", Size: 200, StoredAt: t0},
	)
	for _, tt := range []struct {
		q     port.MediaQuery
		want  []string
		total int
		bytes int64
	}{
		{port.MediaQuery{Page: 1, Sort: port.MediaByStored}, []string{"b", "c", "a", "d"}, 4, 900},
		{port.MediaQuery{Page: 1, Sort: port.MediaByIdle}, []string{"a", "d", "c", "b"}, 4, 900},
		{port.MediaQuery{Page: 1, Sort: port.MediaBySize}, []string{"b", "a", "d", "c"}, 4, 900},
		{port.MediaQuery{Page: 2, Sort: port.MediaBySize}, []string{}, 4, 900},
		{port.MediaQuery{Page: math.MaxInt, Sort: port.MediaBySize}, []string{}, 4, 900},
		{port.MediaQuery{Page: 1, Sort: port.MediaByStored, Title: "50%_off"}, []string{"a"}, 1, 300},
		{port.MediaQuery{Page: 1, Sort: port.MediaByStored, Title: "OFF"}, []string{"b", "c", "a"}, 3, 700},
	} {
		page, err := lib.ListStoredMedia(t.Context(), tt.q)
		if got := mediaKeys(page.Items); err != nil || !slices.Equal(got, tt.want) || page.Total != tt.total || page.TotalBytes != tt.bytes {
			t.Errorf("%+v = %q，共 %d 首 %d 字节，%v，期望 %q，共 %d 首 %d 字节", tt.q, got, page.Total, page.TotalBytes, err, tt.want, tt.total, tt.bytes)
		}
	}

	var many []port.StoredMedia
	for i := range port.MediaPageSize + 1 {
		many = append(many, port.StoredMedia{Key: fmt.Sprintf("key%02d", i), Link: fmt.Sprintf("https://example.com/%02d", i), Size: 1, StoredAt: t0})
	}
	lib = NewMediaLibrary(many...)
	var listed []port.StoredMedia
	for p, want := range []int{port.MediaPageSize, 1, 0} {
		page, err := lib.ListStoredMedia(t.Context(), port.MediaQuery{Page: p + 1, Sort: port.MediaByStored})
		if err != nil || len(page.Items) != want || page.Total != len(many) {
			t.Fatalf("第 %d 页 = %d 首，共 %d 首，%v，期望 %d 首，共 %d 首", p+1, len(page.Items), page.Total, err, want, len(many))
		}
		listed = append(listed, page.Items...)
	}
	if got, want := mediaKeys(listed), mediaKeys(many); !slices.Equal(got, want) {
		t.Errorf("依次翻页 = %q，期望按曲目 ID 排的 %q", got, want)
	}
}
