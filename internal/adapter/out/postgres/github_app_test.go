package postgres

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

func TestGitHubOAuthApp(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	if app, err := store.GitHubOAuthApp(ctx); err != nil || app != (port.GitHubOAuthApp{}) {
		t.Fatalf("没有配置时 = %#v, %v，期望零值", app, err)
	}
	if err := store.SetNeteaseCookie(ctx, "MUSIC_U=1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []port.GitHubOAuthApp{{ClientID: "client-1", ClientSecret: "secret-1"}, {ClientID: "client-2", ClientSecret: "secret-2"}} {
		if err := store.SetGitHubOAuthApp(ctx, want); err != nil {
			t.Fatal(err)
		}
		if got, err := store.GitHubOAuthApp(ctx); err != nil || got != want {
			t.Errorf("GitHubOAuthApp = %#v, %v，期望 %#v", got, err, want)
		}
	}

	// 并发地保存和读取：读到的 Client ID 和 Secret 总是同一次保存的。
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 20 {
				app := port.GitHubOAuthApp{ClientID: fmt.Sprintf("client-%d-%d", i, j), ClientSecret: fmt.Sprintf("secret-%d-%d", i, j)}
				if err := store.SetGitHubOAuthApp(ctx, app); err != nil {
					t.Error(err)
					return
				}
			}
		})
		wg.Go(func() {
			for range 20 {
				got, err := store.GitHubOAuthApp(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if strings.TrimPrefix(got.ClientID, "client-") != strings.TrimPrefix(got.ClientSecret, "secret-") {
					t.Errorf("读到的 Client ID 和 Secret 不是同一次保存的：%#v", got)
				}
			}
		})
	}
	wg.Wait()

	if err := store.DeleteGitHubOAuthApp(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GitHubOAuthApp(ctx); err != nil || got != (port.GitHubOAuthApp{}) {
		t.Errorf("清除之后 = %#v, %v，期望零值", got, err)
	}
	if err := store.DeleteGitHubOAuthApp(ctx); err != nil {
		t.Errorf("没有配置时清除 = %v，期望什么也不做", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM credentials").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if cookie, err := store.NeteaseCookie(ctx); err != nil || cookie != "MUSIC_U=1" || rows != 1 {
		t.Errorf("清除之后网易云 Cookie = %q, %v，凭据表有 %d 行，期望只删除 GitHub 的一行", cookie, err, rows)
	}
}

// 存储中的值不是保存时的格式时报错，错误信息中不带原值：其中可能有 Client Secret。
func TestGitHubOAuthAppRejectsCorruptValue(t *testing.T) {
	store, pool := newStore(t)
	for _, value := range []string{`not json secret-1`, `{"clientId":"client-1"}`, `{"clientSecret":"secret-1"}`} {
		if _, err := pool.Exec(t.Context(), "INSERT INTO credentials (name, value) VALUES ('github', $1) ON CONFLICT (name) DO UPDATE SET value = excluded.value", value); err != nil {
			t.Fatal(err)
		}
		_, err := store.GitHubOAuthApp(t.Context())
		if err == nil {
			t.Errorf("存储的值为 %q 时应报错", value)
			continue
		}
		if strings.Contains(err.Error(), "secret-1") {
			t.Errorf("错误信息中有原值：%v", err)
		}
	}
}
