package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/protocol"
)

func TestSite(t *testing.T) {
	store := porttest.New()
	server, logs := start(t, services(store, store, "admin-secret"))
	brandName := func(name string) response {
		return response{status: http.StatusOK, body: `{"brandName":"` + name + `"}`}
	}

	if got := send(t, server, http.MethodGet, "/api/site", ""); got != brandName("Alisten") {
		t.Errorf("没有设置过时 GET /api/site = %#v，期望默认的 Alisten", got)
	}
	if got := send(t, server, http.MethodPatch, "/api/admin/site", `{"brandName":" 周末听歌会 "}`, adminHeader...); got != brandName("周末听歌会") {
		t.Errorf("PATCH /api/admin/site = %#v，期望返回去掉首尾空白之后存下的名字", got)
	}
	if got := send(t, server, http.MethodGet, "/api/site", ""); got != brandName("周末听歌会") {
		t.Errorf("修改之后 GET /api/site = %#v，期望去掉首尾空白的新名字", got)
	}

	invalid := failure(protocol.ErrorCodeInvalidArgument, "brandName：去掉首尾空白后应有 1 到 20 个字符，其中至少一个看得见，不能包含控制字符")
	for _, name := range []string{"  ", strings.Repeat("歌", 21)} {
		if got := send(t, server, http.MethodPatch, "/api/admin/site", `{"brandName":"`+name+`"}`, adminHeader...); got != invalid {
			t.Errorf("PATCH 品牌名 %q = %#v，期望 %#v", name, got, invalid)
		}
	}
	unauthorized := failure(protocol.ErrorCodeUnauthorized, "管理 token 无效")
	if got := send(t, server, http.MethodPatch, "/api/admin/site", `{"brandName":"别的名字"}`); got != unauthorized {
		t.Errorf("不带管理 token 的 PATCH = %#v，期望 %#v", got, unauthorized)
	}
	if got := send(t, server, http.MethodGet, "/api/site", ""); got != brandName("周末听歌会") {
		t.Errorf("被拒绝的修改之后 GET /api/site = %#v，期望不变", got)
	}
	if logs.Len() != 0 {
		t.Errorf("这些请求都不应产生日志：\n%s", logs)
	}
}
