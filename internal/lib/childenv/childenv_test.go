package childenv

import (
	"slices"
	"testing"
)

func TestMinimalKeepsOnlyWhatChildrenNeed(t *testing.T) {
	t.Setenv("PATH", "/usr/local/bin:/usr/bin")
	t.Setenv("XDG_CACHE_HOME", "/var/cache/alisten")
	t.Setenv("DATABASE_PASSWORD", "数据库的密码")
	t.Setenv("STORAGE_SECRET_ACCESS_KEY", "对象存储的密钥")
	t.Setenv("ALISTEN_ADMIN_TOKEN", "管理 token")
	t.Setenv("no_proxy", "*")
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	got := Minimal()
	for _, want := range []string{"PATH=/usr/local/bin:/usr/bin", "XDG_CACHE_HOME=/var/cache/alisten"} {
		if !slices.Contains(got, want) {
			t.Errorf("环境里没有 %s：%q", want, got)
		}
	}
	for _, kv := range got {
		for _, name := range []string{"DATABASE_PASSWORD", "STORAGE_SECRET_ACCESS_KEY", "ALISTEN_ADMIN_TOKEN", "no_proxy", "HTTPS_PROXY"} {
			if len(kv) > len(name) && kv[:len(name)+1] == name+"=" {
				t.Errorf("%s 传给了子进程", name)
			}
		}
	}
}
