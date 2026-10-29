package site

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

func TestBrandName(t *testing.T) {
	s := New(porttest.New())
	if got, err := s.BrandName(t.Context()); err != nil || got != "Alisten" {
		t.Fatalf("没有设置过时 = %q, %v，期望默认的 Alisten", got, err)
	}
	if saved, err := s.SetBrandName(t.Context(), " 周末听歌会\n"); err != nil || saved != "周末听歌会" {
		t.Fatalf("SetBrandName = %q, %v，期望返回去掉首尾空白的 %q", saved, err, "周末听歌会")
	}
	if got, err := s.BrandName(t.Context()); err != nil || got != "周末听歌会" {
		t.Errorf("设置之后 = %q, %v，期望去掉首尾空白的 %q", got, err, "周末听歌会")
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
	if got, err := s.BrandName(t.Context()); err != nil || got != "周末听歌会" {
		t.Errorf("被拒绝之后 = %q, %v，期望仍是 %q", got, err, "周末听歌会")
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

func (brokenSite) BrandName(context.Context) (string, error) {
	return "", errBroken
}

func TestBrandNameReadFailure(t *testing.T) {
	if got, err := New(brokenSite{}).BrandName(t.Context()); !errors.Is(err, errBroken) {
		t.Errorf("读取失败时 = %q, %v，期望返回错误而不是默认的品牌名", got, err)
	}
}
