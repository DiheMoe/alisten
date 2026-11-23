package postgres

import "testing"

func TestQQCookie(t *testing.T) {
	s, _ := newStore(t)
	if got, err := s.QQCookie(t.Context()); err != nil || got != "" {
		t.Fatalf("初始 QQ Cookie = %q, %v", got, err)
	}
	if ok, err := s.SwapQQCookie(t.Context(), "old", "new"); err != nil || ok {
		t.Fatalf("没有记录时的 SwapQQCookie = %v, %v，期望 false", ok, err)
	}
	if err := s.SetQQCookie(t.Context(), "uin=o1; qm_keyst=v1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SwapQQCookie(t.Context(), "uin=o1; qm_keyst=other", "uin=o1; qm_keyst=v2"); err != nil || ok {
		t.Fatalf("旧值不匹配时的 SwapQQCookie = %v, %v，期望 false", ok, err)
	}
	if ok, err := s.SwapQQCookie(t.Context(), "uin=o1; qm_keyst=v1", "uin=o1; qm_keyst=v2"); err != nil || !ok {
		t.Fatalf("旧值匹配时的 SwapQQCookie = %v, %v，期望 true", ok, err)
	}
	if got, err := s.QQCookie(t.Context()); err != nil || got != "uin=o1; qm_keyst=v2" {
		t.Errorf("Swap 后的 QQ Cookie = %q, %v", got, err)
	}
	if err := s.SetQQCookie(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.QQCookie(t.Context()); err != nil || got != "" {
		t.Errorf("清空后的 QQ Cookie = %q, %v，期望空字符串", got, err)
	}
}
