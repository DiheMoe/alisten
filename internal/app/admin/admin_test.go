package admin

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/room"
)

type fixture struct {
	admin    *Service
	store    *porttest.Store
	youtube  *porttest.YouTubeCookies
	netease  *porttest.NetEaseQRLogin
	qq       *porttest.QQQRLogin
	bilibili *porttest.BilibiliQRLogin
	node     *porttest.Node
	host     *realtime.Host
	gateway  *realtime.Gateway
}

func newService(t *testing.T, token string) fixture {
	t.Helper()
	store := porttest.New()
	node := porttest.NewRedis().Node("a")
	host, gateway := realtime.New(store, store, nil, porttest.NewPreparer(), realtime.Cluster{Instance: "a", Leases: node, Liveness: node, Bus: node}, slog.New(slog.DiscardHandler))
	ctx, stop := context.WithCancel(context.Background())
	wait, err := realtime.Start(ctx, host, gateway)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if err := gateway.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		stop()
		wait()
	})
	l := lobby.New(store, node)
	netease, qq, bilibili, youtube := porttest.NewNetEaseQRLogin(), porttest.NewQQQRLogin(), porttest.NewBilibiliQRLogin(), porttest.NewYouTubeCookies()
	return fixture{admin: New(token, l, store, store, store, netease, qq, bilibili, youtube, gateway), store: store, youtube: youtube, netease: netease, qq: qq, bilibili: bilibili, node: node, host: host, gateway: gateway}
}

func TestSetYouTubeCookies(t *testing.T) {
	f := newService(t, "secret")

	// Netscape 格式原样保存，换行统一成 \n；#HttpOnly_ 开头的是有效 cookie 行，不是注释。
	input := "# Netscape HTTP Cookie File\r\n#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t1798765432\tSID\tfictional\r\n.youtube.com\tTRUE\t/\tFALSE\t1798765432\tHSID\tother\r\n"
	want := "# Netscape HTTP Cookie File\n#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t1798765432\tSID\tfictional\n.youtube.com\tTRUE\t/\tFALSE\t1798765432\tHSID\tother"
	if err := f.admin.SetYouTubeCookies(t.Context(), input); err != nil {
		t.Fatalf("保存 Netscape cookies：%v", err)
	}
	if got := f.youtube.Cookies(); got != want {
		t.Errorf("保存的 cookies = %q，期望 %q", got, want)
	}

	// 请求头格式转成 Netscape：带和不带 Cookie: 前缀；cookie 名字保留原始大小写。
	for _, tc := range []struct{ header, want string }{
		{"SID=fictional; HSID=other", "\tSID\tfictional"},
		{"Cookie: SID=fictional; HSID=other", "\tSID\tfictional"},
		{"cookie:sid=fictional", "\tsid\tfictional"},
	} {
		if err := f.admin.SetYouTubeCookies(t.Context(), tc.header); err != nil {
			t.Fatalf("保存请求头格式的 cookies：%v", err)
		}
		saved := f.youtube.Cookies()
		lines := strings.Split(saved, "\n")
		if lines[0] != "# Netscape HTTP Cookie File" {
			t.Errorf("转换后的第一行 = %q，期望 Netscape 头", lines[0])
		}
		for _, line := range lines[1:] {
			fields := strings.Split(line, "\t")
			if len(fields) != 7 || fields[0] != ".youtube.com" || fields[1] != "TRUE" || fields[2] != "/" || fields[3] != "TRUE" {
				t.Errorf("转换后的行 = %q，期望 .youtube.com、Secure 的 Netscape 行", line)
				continue
			}
			expiry, err := strconv.ParseInt(fields[4], 10, 64)
			if err != nil || expiry <= time.Now().Unix() {
				t.Errorf("过期时间 = %q，期望晚于现在", fields[4])
			}
		}
		if !strings.Contains(saved, tc.want) {
			t.Errorf("转换后的内容缺少 %q 行：%q", tc.want, saved)
		}
	}

	// 无法识别的内容返回固定错误，错误文本不包含输入的值。
	unrecognized := []string{"fictional", "  \n ", "name\twith\ttabs", "a=1\nb=2", "# Netscape HTTP Cookie File"}
	for _, input := range unrecognized {
		err := f.admin.SetYouTubeCookies(t.Context(), input)
		if !errors.Is(err, ErrUnrecognizedCookies) {
			t.Errorf("保存 %q 的错误 = %v，期望 ErrUnrecognizedCookies", input, err)
		}
		if strings.Contains(err.Error(), input) && strings.TrimSpace(input) != "" {
			t.Errorf("错误文本包含输入的值：%v", err)
		}
	}

	failure := errors.New("保存失败")
	f.youtube.Fail(failure)
	if err := f.admin.SetYouTubeCookies(t.Context(), "session=fictional"); !errors.Is(err, failure) {
		t.Errorf("保存失败时的错误 = %v，期望透传 %v", err, failure)
	}
}

func TestClearYouTubeCookies(t *testing.T) {
	f := newService(t, "secret")
	if err := f.admin.SetYouTubeCookies(t.Context(), "session=fictional"); err != nil {
		t.Fatal(err)
	}
	if err := f.admin.ClearYouTubeCookies(t.Context()); err != nil {
		t.Fatalf("清除 cookies：%v", err)
	}
	if got := f.youtube.Cookies(); got != "" {
		t.Errorf("清除之后的 cookies = %q，期望空字符串", got)
	}

	failure := errors.New("删除失败")
	f.youtube.Fail(failure)
	if err := f.admin.ClearYouTubeCookies(t.Context()); !errors.Is(err, failure) {
		t.Errorf("删除失败时的错误 = %v，期望透传 %v", err, failure)
	}
}

func (f fixture) enter(t *testing.T, roomID, name string) ([]byte, *realtime.Session) {
	t.Helper()
	hash := sha256.Sum256([]byte(name))
	m, err := f.store.CreateMember(t.Context(), hash[:], name)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.store.FindRoom(t.Context(), roomID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateTicket(t.Context(), port.NewTicket{TokenHash: hash[:], RoomID: roomID, MemberID: m.ID, PasswordHash: r.PasswordHash}); err != nil {
		t.Fatal(err)
	}
	session, err := f.gateway.Join(t.Context(), roomID, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.gateway.Leave(session) })
	return hash[:], session
}

// 会话没有关闭时返回 0。
func closeReason(t *testing.T, s *realtime.Session) realtime.CloseReason {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for {
		m, reason, _ := s.Next(ctx)
		if m == nil {
			return reason
		}
	}
}

// 票据作废的通知经由房间频道送达，等会话关闭之后再看原因。
func waitClosed(t *testing.T, s *realtime.Session) realtime.CloseReason {
	t.Helper()
	select {
	case <-s.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("会话没有关闭")
	}
	return closeReason(t, s)
}

func ptr(s string) *string { return &s }

func TestAuthorize(t *testing.T) {
	s := newService(t, "secret").admin
	if err := s.Authorize("secret"); err != nil {
		t.Errorf("正确的管理 token 被拒绝：%v", err)
	}
	for _, token := range []string{"", "secre", "secret ", "Secret"} {
		if err := s.Authorize(token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Authorize(%q) = %v，期望 ErrInvalidToken", token, err)
		}
	}
}

func TestUpdateRoom(t *testing.T) {
	f := newService(t, "secret")
	id, err := f.admin.CreateRoom(t.Context(), NewRoom{Name: "大厅", Description: "常驻", Slug: "hall"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.CreateRoom(t.Context(), NewRoom{Name: "二厅", Slug: "lounge"}); err != nil {
		t.Fatal(err)
	}
	ticket, session := f.enter(t, id, "alice")

	if err := f.admin.UpdateRoom(t.Context(), id, RoomChange{Name: ptr("新大厅")}); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.store.FindRoom(t.Context(), id); r.Info.Name != "新大厅" || r.Info.Description != "常驻" || r.Slug != "hall" {
		t.Errorf("只改名称之后 = %+v，期望描述和别名不变", r)
	}
	if err := f.admin.UpdateRoom(t.Context(), id, RoomChange{Name: ptr("改不了"), Slug: ptr("lounge")}); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("别名被占用时的错误 = %v，期望 ErrSlugTaken", err)
	}
	if r, _ := f.store.FindRoom(t.Context(), id); r.Info.Name != "新大厅" || r.Slug != "hall" {
		t.Errorf("别名被占用之后 = %+v，期望什么都没有改", r)
	}

	if err := f.admin.UpdateRoom(t.Context(), id, RoomChange{Password: ptr("芝麻开门"), Description: ptr("上锁了")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TicketHolder(t.Context(), ticket); !errors.Is(err, port.ErrTicketNotFound) {
		t.Errorf("设置密码之后票据的错误 = %v，期望 ErrTicketNotFound", err)
	}
	if reason := waitClosed(t, session); reason != realtime.CloseRevoked {
		t.Errorf("设置密码之后会话的关闭原因 = %d，期望 CloseRevoked", reason)
	}
	if r, _ := f.store.FindRoom(t.Context(), id); !strings.HasPrefix(r.PasswordHash, "$argon2id$") || r.Info.Description != "上锁了" {
		t.Errorf("设置密码和描述之后 = %+v", r)
	}
}

func TestUpdateRoomKeepsTicketsWhenRoomUnavailable(t *testing.T) {
	f := newService(t, "secret")
	id, err := f.admin.CreateRoom(t.Context(), NewRoom{Name: "大厅"})
	if err != nil {
		t.Fatal(err)
	}
	ticket, session := f.enter(t, id, "alice")
	if err := f.host.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := f.admin.UpdateRoom(t.Context(), id, RoomChange{Name: ptr("新大厅"), Password: ptr("芝麻开门")}); !errors.Is(err, realtime.ErrUnavailable) {
		t.Fatalf("房间不可用时的错误 = %v，期望 ErrUnavailable", err)
	}
	if _, err := f.store.TicketHolder(t.Context(), ticket); err != nil {
		t.Errorf("修改失败之后票据的错误 = %v，期望票据仍然有效", err)
	}
	if reason := closeReason(t, session); reason != 0 {
		t.Errorf("修改失败之后会话以 %d 关闭，期望不受影响", reason)
	}
	if r, _ := f.store.FindRoom(t.Context(), id); r.PasswordHash != "" || r.Info.Name != "大厅" {
		t.Errorf("修改失败之后 = %+v，期望密码和名称都没有改", r)
	}
}

func TestCancelPasswordKeepsTickets(t *testing.T) {
	f := newService(t, "secret")
	id, err := f.admin.CreateRoom(t.Context(), NewRoom{Name: "大厅", Password: "芝麻开门"})
	if err != nil {
		t.Fatal(err)
	}
	ticket, session := f.enter(t, id, "alice")

	if err := f.admin.UpdateRoom(t.Context(), id, RoomChange{Password: ptr("")}); err != nil {
		t.Fatal(err)
	}
	if holder, err := f.store.TicketHolder(t.Context(), ticket); err != nil || holder.RoomID != id {
		t.Errorf("取消密码之后 TicketHolder = %+v, %v，期望票据仍然有效", holder, err)
	}
	if reason := closeReason(t, session); reason != 0 {
		t.Errorf("取消密码之后会话以 %d 关闭，期望不受影响", reason)
	}
	if r, _ := f.store.FindRoom(t.Context(), id); r.PasswordHash != "" {
		t.Errorf("取消密码之后的密码哈希 = %q", r.PasswordHash)
	}
}

func TestRoomAPIKey(t *testing.T) {
	f := newService(t, "secret")
	id, err := f.admin.CreateRoom(t.Context(), NewRoom{Name: "大厅"})
	if err != nil {
		t.Fatal(err)
	}
	holder := func(key string) (port.TicketHolder, error) {
		hash := sha256.Sum256([]byte(key))
		return f.store.TicketHolder(t.Context(), hash[:])
	}
	status := func() port.APIKeyStatus {
		t.Helper()
		s, err := f.admin.RoomAPIKeyStatus(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	if got := status(); got.Set || !got.CreatedAt.IsZero() {
		t.Errorf("还没有 Key 时的状态 = %+v，期望未生成", got)
	}
	before := time.Now()
	key, err := f.admin.ResetRoomAPIKey(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := holder(key)
	if err != nil || bot.RoomID != id || !bot.Bot {
		t.Fatalf("Key 的持有者 = %+v, %v，期望这个房间的 bot 成员", bot, err)
	}
	if got := status(); !got.Set || got.CreatedAt.Before(before) || got.CreatedAt.After(time.Now()) {
		t.Errorf("生成之后的状态 = %+v，期望已生成，时间是这次生成的时间", got)
	}

	reset, err := f.admin.ResetRoomAPIKey(t.Context(), id)
	if err != nil || reset == key {
		t.Fatalf("重置 = %q, %v，期望换成新的 Key", reset, err)
	}
	if _, err := holder(key); !errors.Is(err, port.ErrTicketNotFound) {
		t.Errorf("重置之后旧 Key 的错误 = %v，期望 ErrTicketNotFound", err)
	}
	if got, err := holder(reset); err != nil || got != bot {
		t.Errorf("重置之后新 Key 的持有者 = %+v, %v，期望 bot 成员不变", got, err)
	}

	ticket, _ := f.enter(t, id, "alice")
	for _, password := range []string{"芝麻开门", "新密码", ""} {
		if err := f.admin.UpdateRoom(t.Context(), id, RoomChange{Password: ptr(password)}); err != nil {
			t.Fatal(err)
		}
		if got, err := holder(reset); err != nil || got != bot {
			t.Errorf("密码改为 %q 之后 Key 的持有者 = %+v, %v，期望 Key 仍然有效", password, got, err)
		}
	}
	if _, err := f.store.TicketHolder(t.Context(), ticket); !errors.Is(err, port.ErrTicketNotFound) {
		t.Errorf("设置密码之后成员票据的错误 = %v，期望 ErrTicketNotFound", err)
	}

	ephemeral, err := f.store.CreateRoom(t.Context(), port.NewRoom{Kind: room.Ephemeral, Name: "临时"})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"查看": func() error { _, err := f.admin.RoomAPIKeyStatus(t.Context(), ephemeral); return err },
		"生成": func() error { _, err := f.admin.ResetRoomAPIKey(t.Context(), ephemeral); return err },
	} {
		if err := call(); !errors.Is(err, port.ErrRoomNotFound) {
			t.Errorf("%s临时房间的 Key 的错误 = %v，期望 ErrRoomNotFound", name, err)
		}
	}
}

func TestCredentialStatus(t *testing.T) {
	f := newService(t, "secret")
	name, vip := "测试账号", true
	for _, source := range []struct {
		name        string
		save        func(context.Context, string) error
		status      func(context.Context) (port.CredentialStatus, error)
		setAccount  func(*port.SourceAccount)
		failAccount func(error)
		account     *port.SourceAccount
	}{
		{"网易云", f.store.SetNeteaseCookie, f.admin.NeteaseCredentialStatus, f.netease.SetAccount, f.netease.FailAccount, &port.SourceAccount{ID: "123", Name: &name, VIP: &vip, Verified: true}},
		{"QQ", f.store.SetQQCookie, f.admin.QQCredentialStatus, f.qq.SetAccount, f.qq.FailAccount, &port.SourceAccount{ID: "456"}},
	} {
		t.Run(source.name, func(t *testing.T) {
			source.failAccount(errors.New("无凭据时不允许查询账号"))
			got, err := source.status(t.Context())
			if err != nil || got.Set || got.Account != nil {
				t.Fatalf("没有凭据时的状态 = %+v, %v，期望未设置且不查询账号", got, err)
			}
			if err := source.save(t.Context(), "敏感凭据"); err != nil {
				t.Fatal(err)
			}
			source.setAccount(source.account)
			got, err = source.status(t.Context())
			if err != nil || !got.Set || got.Account != source.account {
				t.Fatalf("保存凭据后的状态 = %+v, %v，期望 %+v", got, err, source.account)
			}
			upstream := errors.New("上游失败")
			source.failAccount(upstream)
			if _, err := source.status(t.Context()); !errors.Is(err, port.ErrProviderFailed) || !errors.Is(err, upstream) {
				t.Fatalf("账号查询错误 = %v，期望包含上游错误和 ErrProviderFailed", err)
			}
		})
	}
}

func TestQRLogin(t *testing.T) {
	f := newService(t, "secret")
	netease, err := f.admin.CreateNeteaseQRLogin(t.Context())
	if err != nil || netease.Key == "" || netease.LoginURL == "" {
		t.Fatalf("网易云的二维码 = %+v, %v", netease, err)
	}
	scanned := port.QRLoginResult{Status: port.QRLoginScanned, Message: "请在手机上确认"}
	f.netease.SetResult(netease.Key, scanned)
	if got, err := f.admin.CheckNeteaseQRLogin(t.Context(), netease.Key); err != nil || got != scanned {
		t.Errorf("网易云的扫码结果 = %+v, %v，期望 %+v", got, err, scanned)
	}

	wx, err := f.admin.CreateQQQRLogin(t.Context(), port.QQLoginWeChat)
	if err != nil || wx.QRImage != porttest.WeChatQRImage {
		t.Fatalf("微信扫码的二维码 = %+v, %v，期望音源给出的微信二维码", wx, err)
	}
	for typ, want := range map[port.QQLoginType]port.QRLoginStatus{port.QQLoginWeChat: port.QRLoginWaiting, port.QQLoginQQ: port.QRLoginExpired} {
		if got, err := f.admin.CheckQQQRLogin(t.Context(), typ, wx.Key); err != nil || got.Status != want {
			t.Errorf("按 %s 查询微信扫码的结果 = %+v, %v，期望 %s", typ, got, err, want)
		}
	}

	f.netease.Fail(errors.New("连接被重置"))
	f.qq.Fail(errors.New("连接被重置"))
	for name, call := range map[string]func() error{
		"生成网易云的二维码":   func() error { _, err := f.admin.CreateNeteaseQRLogin(t.Context()); return err },
		"查询网易云的扫码结果":  func() error { _, err := f.admin.CheckNeteaseQRLogin(t.Context(), netease.Key); return err },
		"生成 QQ 的二维码":  func() error { _, err := f.admin.CreateQQQRLogin(t.Context(), port.QQLoginQQ); return err },
		"查询 QQ 的扫码结果": func() error { _, err := f.admin.CheckQQQRLogin(t.Context(), port.QQLoginWeChat, wx.Key); return err },
	} {
		if err := call(); !errors.Is(err, port.ErrProviderFailed) {
			t.Errorf("音源出错时%s = %v，期望 ErrProviderFailed", name, err)
		}
	}
}

func TestBilibiliCredentialStatus(t *testing.T) {
	f := newService(t, "secret")
	if got, err := f.admin.BilibiliCredentialStatus(t.Context()); err != nil || got.Set || got.Account != nil {
		t.Fatalf("没有登录时 = %+v, %v，期望未设置", got, err)
	}
	name, vip := "测试账号", true
	account := &port.SourceAccount{ID: "789", Name: &name, VIP: &vip, Verified: true}
	f.bilibili.SetCredential(account)
	if got, err := f.admin.BilibiliCredentialStatus(t.Context()); err != nil || !got.Set || got.Account != account {
		t.Fatalf("登录之后 = %+v, %v，期望 %+v", got, err, account)
	}
	upstream := errors.New("上游失败")
	f.bilibili.FailStatus(upstream)
	if _, err := f.admin.BilibiliCredentialStatus(t.Context()); !errors.Is(err, port.ErrProviderFailed) || !errors.Is(err, upstream) {
		t.Errorf("查询登录状态失败时 = %v，期望包含音源的错误和 ErrProviderFailed", err)
	}
}

func TestBilibiliQRLogin(t *testing.T) {
	f := newService(t, "secret")
	session, err := f.admin.CreateBilibiliQRLogin(t.Context())
	if err != nil || session.Key == "" || session.LoginURL == "" || session.QRImage != "" {
		t.Fatalf("B 站的二维码 = %+v, %v，期望只有登录链接", session, err)
	}
	scanned := port.QRLoginResult{Status: port.QRLoginScanned}
	f.bilibili.SetResult(session.Key, scanned)
	if got, err := f.admin.CheckBilibiliQRLogin(t.Context(), session.Key); err != nil || got != scanned {
		t.Errorf("B 站的扫码结果 = %+v, %v，期望 %+v", got, err, scanned)
	}
	f.bilibili.Fail(errors.New("连接被重置"))
	for name, call := range map[string]func() error{
		"生成 B 站的二维码":  func() error { _, err := f.admin.CreateBilibiliQRLogin(t.Context()); return err },
		"查询 B 站的扫码结果": func() error { _, err := f.admin.CheckBilibiliQRLogin(t.Context(), session.Key); return err },
	} {
		if err := call(); !errors.Is(err, port.ErrProviderFailed) {
			t.Errorf("音源出错时%s = %v，期望 ErrProviderFailed", name, err)
		}
	}
}

func TestUpdateRoomReportsFailedRevoke(t *testing.T) {
	f := newService(t, "secret")
	id, err := f.admin.CreateRoom(t.Context(), NewRoom{Name: "大厅"})
	if err != nil {
		t.Fatal(err)
	}
	f.node.SetDown(true)
	err = f.admin.UpdateRoom(t.Context(), id, RoomChange{Password: ptr("芝麻开门")})
	f.node.SetDown(false)
	if !errors.Is(err, realtime.ErrUnavailable) {
		t.Errorf("通知发不出去时的错误 = %v，期望 ErrUnavailable", err)
	}
	if r, err := f.store.FindRoom(t.Context(), id); err != nil || r.PasswordHash == "" {
		t.Errorf("通知之前密码已经设置好，房间 = %#v, %v", r, err)
	}
}

func TestResetRoomAPIKeyReportsFailedRevoke(t *testing.T) {
	f := newService(t, "secret")
	id, err := f.admin.CreateRoom(t.Context(), NewRoom{Name: "大厅"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.admin.ResetRoomAPIKey(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	f.node.SetDown(true)
	renewed, err := f.admin.ResetRoomAPIKey(t.Context(), id)
	f.node.SetDown(false)
	if renewed != "" || !errors.Is(err, realtime.ErrUnavailable) {
		t.Errorf("通知发不出去时 = %q, %v，期望不返回 Key，错误为 ErrUnavailable", renewed, err)
	}
	hash := sha256.Sum256([]byte(key))
	if _, err := f.store.TicketHolder(t.Context(), hash[:]); !errors.Is(err, port.ErrTicketNotFound) {
		t.Errorf("通知之前旧 Key 已经失效，它的错误 = %v，期望 ErrTicketNotFound", err)
	}
}
