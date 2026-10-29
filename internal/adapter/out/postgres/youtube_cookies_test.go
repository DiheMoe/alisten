package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestYouTubeCookies(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()

	status, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Set || !status.UpdatedAt.IsZero() {
		t.Fatalf("初始状态 = %+v，期望未设置且时间为零值", status)
	}

	before := time.Now()
	if err := s.SetYouTubeCookies(ctx, "session=fictional"); err != nil {
		t.Fatal(err)
	}
	first, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	if !first.Set {
		t.Fatal("写入后状态仍为未设置")
	}
	if first.UpdatedAt.Before(before.Add(-time.Second)) || first.UpdatedAt.After(after.Add(time.Second)) {
		t.Fatalf("更新时间 = %s，期望接近当前时间 [%s, %s]", first.UpdatedAt, before, after)
	}

	time.Sleep(2 * time.Millisecond)
	if err := s.SetCookies(ctx, "session=replacement"); err != nil {
		t.Fatal(err)
	}
	second, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Set || !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("覆盖后状态 = %+v，上次更新时间 = %s，期望已设置且时间更新", second, first.UpdatedAt)
	}
	cookies, err := s.YouTubeCookies(ctx)
	if err != nil || cookies != "session=replacement" {
		t.Fatalf("读取 cookies = %q, %v，期望最近一次写入的内容", cookies, err)
	}

	if err := s.SetYouTubeCookies(ctx, ""); err != nil {
		t.Fatal(err)
	}
	status, err = s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Set || !status.UpdatedAt.IsZero() {
		t.Fatalf("删除后状态 = %+v，期望未设置且时间为零值", status)
	}
	cookies, err = s.YouTubeCookies(ctx)
	if err != nil || cookies != "" {
		t.Fatalf("删除后读取 cookies = %q, %v，期望空字符串", cookies, err)
	}
	if _, err := s.q.Credential(ctx, "youtube"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("空字符串写入后凭据行仍存在，查询错误 = %v", err)
	}
}
