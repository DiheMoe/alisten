package postgres

import "testing"

func TestBilibiliCredential(t *testing.T) {
	s, _ := newStore(t)
	if got, err := s.BilibiliCredential(t.Context()); err != nil || got != "" {
		t.Fatalf("初始 Bilibili 凭据 = %q, %v", got, err)
	}
	for _, value := range []string{`{"cookie":"SESSDATA=x","refresh_token":"y"}`, ""} {
		if err := s.SetBilibiliCredential(t.Context(), value); err != nil {
			t.Fatal(err)
		}
		if got, err := s.BilibiliCredential(t.Context()); err != nil || got != value {
			t.Errorf("Bilibili 凭据 = %q, %v，期望 %q", got, err, value)
		}
	}
}
