package postgres

import "testing"

func TestBilibiliCookie(t *testing.T) {
	s, _ := newStore(t)
	if got, err := s.BilibiliCookie(t.Context()); err != nil || got != "" {
		t.Fatalf("初始 B 站 Cookie = %q, %v", got, err)
	}
	for _, value := range []string{"SESSDATA=x; bili_jct=y", ""} {
		if err := s.SetBilibiliCookie(t.Context(), value); err != nil {
			t.Fatal(err)
		}
		if got, err := s.BilibiliCookie(t.Context()); err != nil || got != value {
			t.Errorf("B 站 Cookie = %q, %v，期望 %q", got, err, value)
		}
	}
}
