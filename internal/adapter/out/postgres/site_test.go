package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dihemoe/alisten/internal/app/port"
)

func TestSiteSettings(t *testing.T) {
	store, pool := newStore(t)
	check := func(want port.SiteSettings, why string) {
		t.Helper()
		if got, err := store.Settings(t.Context()); err != nil || got != want {
			t.Errorf("%s：Settings = %#v, %v，期望 %#v", why, got, err, want)
		}
	}
	check(port.SiteSettings{RegistrationOpen: true}, "没有设置过时没有品牌名、开放注册")
	if err := store.SetRegistrationOpen(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	check(port.SiteSettings{RegistrationOpen: false}, "只关闭注册时品牌名仍未设置")
	for _, name := range []string{"周末听歌会", "Alisten"} {
		if err := store.SetBrandName(t.Context(), name); err != nil {
			t.Fatal(err)
		}
		check(port.SiteSettings{BrandName: name, RegistrationOpen: false}, "修改品牌名不影响注册开关")
	}
	if err := store.SetRegistrationOpen(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	check(port.SiteSettings{BrandName: "Alisten", RegistrationOpen: true}, "重新开放注册不影响品牌名")
	if err := store.SetEphemeralRoomCapacity(t.Context(), 32); err != nil {
		t.Fatal(err)
	}
	check(port.SiteSettings{BrandName: "Alisten", RegistrationOpen: true, EphemeralRoomCapacity: 32}, "修改临时房间最多人数不影响品牌名和注册开关")
	if err := store.SetBrandName(t.Context(), "周末听歌会"); err != nil {
		t.Fatal(err)
	}
	check(port.SiteSettings{BrandName: "周末听歌会", RegistrationOpen: true, EphemeralRoomCapacity: 32}, "修改品牌名不影响临时房间最多人数")
	var pgErr *pgconn.PgError
	if err := store.SetEphemeralRoomCapacity(t.Context(), 129); !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "site_ephemeral_room_capacity_check" {
		t.Errorf("临时房间最多人数设成 129 的错误 = %v，期望违反范围检查", err)
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
