package netease

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

func TestCurrentAccount(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		id       string
		nickname string
		vip      bool
		empty    bool
		wantErr  bool
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
			name:  "成功但没有账号资料",
			body:  `{"code":200,"profile":{}}`,
			empty: true,
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
			got, err := client.CurrentAccount(t.Context())
			if tc.wantErr {
				if err == nil {
					t.Fatal("账号查询错误 = nil，期望失败")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.empty {
				if got != nil {
					t.Fatalf("没有账号资料时 = %+v，期望 nil", got)
				}
				return
			}
			var name *string
			if tc.nickname != "" {
				name = &tc.nickname
			}
			want := &port.SourceAccount{ID: tc.id, Name: name, VIP: &tc.vip, Verified: true}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("账号资料 = %+v，期望 %+v", got, want)
			}
		})
	}
}
