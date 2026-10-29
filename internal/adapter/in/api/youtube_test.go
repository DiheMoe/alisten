package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/protocol"
)

func TestAdminYouTubeCookies(t *testing.T) {
	cookies := porttest.NewYouTubeCookies()
	server, _ := startAdmin(t, porttest.NewBilibiliQRLogin(), cookies)

	resp, header := exchange(t, server, http.MethodPut, "/api/admin/credentials/youtube", `{"cookies":"session=fictional"}`, adminHeader...)
	checkNoContent(t, "PUT", resp, header)
	saved := cookies.Cookies()
	if !strings.HasPrefix(saved, "# Netscape HTTP Cookie File\n") || !strings.Contains(saved, "\tsession\tfictional") {
		t.Errorf("保存的 cookies = %q，期望转换后的 Netscape 格式", saved)
	}

	want := failure(protocol.ErrorCodeInvalidArgument, "不是能识别的 cookies：请粘贴扩展导出的 cookies.txt，或开发者工具里请求头 cookie 的值。")
	if got := send(t, server, http.MethodPut, "/api/admin/credentials/youtube", `{"cookies":"fictional"}`, adminHeader...); got != want {
		t.Errorf("PUT 无法识别的 cookies = %#v，期望 %#v", got, want)
	}

	want = failure(protocol.ErrorCodeInvalidArgument, "cookies：长度不能少于 1 个字符")
	if got := send(t, server, http.MethodPut, "/api/admin/credentials/youtube", `{"cookies":""}`, adminHeader...); got != want {
		t.Errorf("PUT 空的 cookies = %#v，期望 %#v", got, want)
	}
	if got := cookies.Cookies(); got != saved {
		t.Errorf("PUT 空的 cookies 之后保存的 cookies = %q，期望不变", got)
	}

	want = failure(protocol.ErrorCodeUnauthorized, "管理 token 无效")
	if got := send(t, server, http.MethodDelete, "/api/admin/credentials/youtube", ""); got != want {
		t.Errorf("不带管理 token 的 DELETE = %#v，期望 %#v", got, want)
	}
	if got := cookies.Cookies(); got != saved {
		t.Errorf("不带管理 token 的 DELETE 之后保存的 cookies = %q，期望不变", got)
	}

	resp, header = exchange(t, server, http.MethodDelete, "/api/admin/credentials/youtube", "", adminHeader...)
	checkNoContent(t, "DELETE", resp, header)
	if got := cookies.Cookies(); got != "" {
		t.Errorf("DELETE 之后保存的 cookies = %q，期望清空", got)
	}

	body := `{"cookies":"` + strings.Repeat("x", 64<<10) + `"}`
	want = failure(protocol.ErrorCodeInvalidArgument, "请求体不能超过 64 KiB")
	if got := send(t, server, http.MethodPut, "/api/admin/credentials/youtube", body, adminHeader...); got != want {
		t.Errorf("超过 64 KiB 的请求 = %#v，期望 %#v", got, want)
	}
}
