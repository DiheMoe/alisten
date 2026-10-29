package postgres

import "testing"

func TestBrandName(t *testing.T) {
	store, pool := newStore(t)
	if name, err := store.BrandName(t.Context()); err != nil || name != "" {
		t.Fatalf("没有设置过时 = %q, %v，期望空字符串", name, err)
	}
	for _, want := range []string{"周末听歌会", "Alisten"} {
		if err := store.SetBrandName(t.Context(), want); err != nil {
			t.Fatal(err)
		}
		if got, err := store.BrandName(t.Context()); err != nil || got != want {
			t.Errorf("BrandName = %q, %v，期望 %q", got, err, want)
		}
	}
	var rows int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM site").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("覆盖写之后 site 表有 %d 行，期望 1 行", rows)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO site (id, brand_name) VALUES (false, '第二行')"); err == nil {
		t.Error("site 表接受了第二行")
	}
}
