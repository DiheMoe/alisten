package postgres

import "testing"

func TestQQCookie(t *testing.T) {
	s, _ := newStore(t)
	if got, err := s.QQCookie(t.Context()); err != nil || got != "" {
		t.Fatalf("初始 QQ Cookie = %q, %v", got, err)
	}
	for _, value := range []string{"uin=o1; qm_keyst=key", ""} {
		if err := s.SetQQCookie(t.Context(), value); err != nil {
			t.Fatal(err)
		}
		if got, err := s.QQCookie(t.Context()); err != nil || got != value {
			t.Errorf("QQ Cookie = %q, %v，期望 %q", got, err, value)
		}
	}
}
