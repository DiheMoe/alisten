package netease

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

func TestCredentialStatus(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		id        string
		nickname  string
		vip       bool
		wantErr   bool
		signedOut bool
	}{
		{
			name:     "会员账号",
			body:     `{"code":200,"profile":{"userId":9007199254740993,"nickname":"测试账号","vipType":1}}`,
			id:       "9007199254740993",
			nickname: "测试账号",
			vip:      true,
		},
		{
			name: "非会员且没有昵称",
			body: `{"code":200,"profile":{"userId":42,"nickname":"","vipType":0}}`,
			id:   "42",
		},
		{
			name:      "登录已失效",
			body:      `{"code":200,"profile":{}}`,
			signedOut: true,
		},
		{
			name:    "业务错误",
			body:    `{"code":301,"profile":{}}`,
			wantErr: true,
		},
		{
			name:    "响应不是 JSON",
			body:    `not-json`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := serve(t, map[string]http.HandlerFunc{
				"/weapi/nuser/account/get": func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(tc.body))
				},
			})
			got, err := client.CredentialStatus(t.Context())
			if tc.wantErr {
				if err == nil {
					t.Fatal("登录状态查询错误 = nil，期望失败")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.signedOut {
				if got != (port.CredentialStatus{Set: true, SignedOut: true}) {
					t.Fatalf("登录失效的状态 = %+v，期望 Set=true 且 SignedOut=true", got)
				}
				return
			}
			var name *string
			if tc.nickname != "" {
				name = &tc.nickname
			}
			want := port.CredentialStatus{
				Set:     true,
				Account: &port.SourceAccount{ID: tc.id, Name: name, VIP: &tc.vip, Verified: true},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("凭据状态 = %+v，期望 %+v", got, want)
			}
		})
	}
}

func TestCredentialStatusWithoutCookie(t *testing.T) {
	c := New(&http.Client{}, func(context.Context) (string, error) { return "", nil }, nil, (&observed{}).record, testLogger)
	got, err := c.CredentialStatus(t.Context())
	if err != nil || got != (port.CredentialStatus{}) {
		t.Fatalf("没有凭据时 = %+v, %v，期望未设置且无错误", got, err)
	}
}
