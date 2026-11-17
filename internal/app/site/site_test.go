package site

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

func TestSettings(t *testing.T) {
	s := New(porttest.New())
	if got, err := s.Settings(t.Context()); err != nil || got != (Settings{BrandName: "Alisten", RegistrationOpen: true, EphemeralRoomCapacity: 16}) {
		t.Fatalf("没有设置过时 = %#v, %v，期望默认的 Alisten、开放注册、临时房间最多 16 人", got, err)
	}
	if saved, err := s.SetBrandName(t.Context(), " 周末听歌会\n"); err != nil || saved != "周末听歌会" {
		t.Fatalf("SetBrandName = %q, %v，期望返回去掉首尾空白的 %q", saved, err, "周末听歌会")
	}
	if err := s.SetRegistrationOpen(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEphemeralRoomCapacity(t.Context(), 40); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Settings(t.Context()); err != nil || got != (Settings{BrandName: "周末听歌会", RegistrationOpen: false, EphemeralRoomCapacity: 40}) {
		t.Errorf("设置之后 = %#v, %v", got, err)
	}
}

// site 不检查范围，因为协议的校验已经保证了范围。越界的值写到存储时被拒绝。
func TestEphemeralRoomCapacityOutOfRangeIsRejectedByStore(t *testing.T) {
	s := New(porttest.New())
	if err := s.SetEphemeralRoomCapacity(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	for _, capacity := range []int{1, 129} {
		if err := s.SetEphemeralRoomCapacity(t.Context(), capacity); err == nil {
			t.Errorf("SetEphemeralRoomCapacity(%d) 成功，期望被存储拒绝", capacity)
		}
	}
	if got, err := s.Settings(t.Context()); err != nil || got.EphemeralRoomCapacity != 2 {
		t.Errorf("被拒绝之后 = %d, %v，期望仍是 2", got.EphemeralRoomCapacity, err)
	}
}

func TestBrandNameCountsCharacters(t *testing.T) {
	s := New(porttest.New())
	for _, name := range []string{strings.Repeat("歌", 20), strings.Repeat("🎵", 20), "  " + strings.Repeat("a", 20) + "  "} {
		if _, err := s.SetBrandName(t.Context(), name); err != nil {
			t.Errorf("SetBrandName(%q) = %v，期望 20 个字符可以保存", name, err)
		}
	}
}

func TestInvalidBrandName(t *testing.T) {
	s := New(porttest.New())
	if _, err := s.SetBrandName(t.Context(), "周末听歌会"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", " \t ", strings.Repeat("歌", 21), "周末\n听歌会", "周末\u0085听歌会"} {
		if _, err := s.SetBrandName(t.Context(), name); !errors.Is(err, ErrInvalidBrandName) {
			t.Errorf("SetBrandName(%q) = %v，期望 ErrInvalidBrandName", name, err)
		}
	}
	if got, err := s.Settings(t.Context()); err != nil || got.BrandName != "周末听歌会" {
		t.Errorf("被拒绝之后 = %q, %v，期望仍是 %q", got.BrandName, err, "周末听歌会")
	}
}

func TestBrandNameNeedsVisibleCharacter(t *testing.T) {
	s := New(porttest.New())
	for _, name := range []string{"\u200b", "\u200b \u200b", "\u200b\u3000\u200b", "\u3000 \u3000"} {
		if _, err := s.SetBrandName(t.Context(), name); !errors.Is(err, ErrInvalidBrandName) {
			t.Errorf("SetBrandName(%q) = %v，期望 ErrInvalidBrandName", name, err)
		}
	}
	if _, err := s.SetBrandName(t.Context(), "\u200b周末听歌会"); err != nil {
		t.Errorf("SetBrandName(%q) = %v，期望有看得见的字符就可以保存", "\u200b周末听歌会", err)
	}
}

type brokenSite struct{ port.Site }

var errBroken = errors.New("连接中断")

func (brokenSite) Settings(context.Context) (port.SiteSettings, error) {
	return port.SiteSettings{}, errBroken
}

func TestSettingsReadFailure(t *testing.T) {
	if got, err := New(brokenSite{}).Settings(t.Context()); !errors.Is(err, errBroken) {
		t.Errorf("读取失败时 = %#v, %v，期望返回错误而不是默认的品牌名", got, err)
	}
}
