package qq

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

func accountTestClient(cookie string, loadErr error) *Client {
	load := func(context.Context) (string, error) { return cookie, loadErr }
	return New(&http.Client{}, load, nil, (&observed{}).record)
}

func TestCurrentAccount(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		wantID string
	}{
		{"QQ uin 去前缀和前导零", "uin=o000123", "123"},
		{"微信 uin", "wxuin=000101", "101"},
		{"ptui_loginuin", "ptui_loginuin=o000102", "102"},
		{"luin", "luin=o000103", "103"},
		{"pt2gguin", "pt2gguin=o000104", "104"},
		{"superuin", "superuin=o000105", "105"},
		{"p_uin", "p_uin=o000106", "106"},
		{"musicid", "musicid=107", "107"},
		{"userid", "userid=108", "108"},
		{"user_id", "user_id=109", "109"},
		{"微信 uin 优先", "uin=000123; wxuin=000456", "456"},
		{"没有账号标识", "qqmusic_key=fictitious", ""},
		{"账号标识为零", "uin=o0000", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := accountTestClient(tc.cookie, nil).CurrentAccount(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantID == "" {
				if got != nil {
					t.Fatalf("没有账号标识时 = %+v，期望 nil", got)
				}
				return
			}
			want := &port.SourceAccount{ID: tc.wantID}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("账号资料 = %+v，期望 %+v", got, want)
			}
		})
	}
}

func TestCurrentAccountCookieError(t *testing.T) {
	want := errors.New("读取失败")
	if _, err := accountTestClient("", want).CurrentAccount(t.Context()); !errors.Is(err, want) {
		t.Fatalf("读取凭据的错误 = %v，期望 %v", err, want)
	}
}
