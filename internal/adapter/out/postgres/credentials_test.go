package postgres

import "testing"

func TestClearedCookieHasNoRow(t *testing.T) {
	store, pool := newStore(t)
	for _, cookie := range []string{"MUSIC_U=1", ""} {
		if err := store.SetNeteaseCookie(t.Context(), cookie); err != nil {
			t.Fatal(err)
		}
	}
	var rows int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM credentials").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if got, err := store.NeteaseCookie(t.Context()); err != nil || got != "" || rows != 0 {
		t.Errorf("清除之后 = %q, %v，凭据表有 %d 行，期望空串和 0 行", got, err, rows)
	}
}

func TestNeteaseCookie(t *testing.T) {
	store, _ := newStore(t)
	if cookie, err := store.NeteaseCookie(t.Context()); err != nil || cookie != "" {
		t.Fatalf("没有设置过时 = %q, %v，期望空字符串", cookie, err)
	}
	for _, want := range []string{"MUSIC_U=1", "MUSIC_U=2"} {
		if err := store.SetNeteaseCookie(t.Context(), want); err != nil {
			t.Fatal(err)
		}
		if got, err := store.NeteaseCookie(t.Context()); err != nil || got != want {
			t.Errorf("NeteaseCookie = %q, %v，期望 %q", got, err, want)
		}
	}
}
