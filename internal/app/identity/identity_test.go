package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

func newService(t *testing.T) (*Service, *porttest.Store) {
	t.Helper()
	store := porttest.New()
	node := porttest.NewRedis().Node("a")
	return New(store, store, store, newGateway(store, node, node)), store
}

// 没有调用 realtime.Start：这些测试不运行房间。
func newGateway(store *porttest.Store, node *porttest.Node, bus port.Bus) *realtime.Gateway {
	_, gateway := realtime.New(store, store, nil, porttest.NewPreparer(), realtime.Cluster{Instance: "a", Leases: node, Liveness: node, Bus: bus}, slog.New(slog.DiscardHandler))
	return gateway
}

func createMember(t *testing.T, s *Service) Session {
	t.Helper()
	session, err := s.CreateMember(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func createRoom(t *testing.T, store *porttest.Store, password string) string {
	t.Helper()
	id, err := store.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "大厅", PasswordHash: hashPassword(t, password)})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ptr(s string) *string { return &s }

func TestCreateMember(t *testing.T) {
	s, store := newService(t)
	session := createMember(t, s)

	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(session.MemberToken) {
		t.Errorf("成员 token %q 应是 256 位随机值的 base64url 编码", session.MemberToken)
	}
	if !regexp.MustCompile(`^游客-[0-9a-f]{4}$`).MatchString(session.Member.Name) || session.Member.AvatarHash != "" {
		t.Errorf("新成员 = %#v，期望默认昵称和空头像", session.Member)
	}
	hash := sha256.Sum256([]byte(session.MemberToken))
	if m, err := store.MemberByToken(t.Context(), hash[:]); err != nil || m != session.Member {
		t.Errorf("存储中应按 token 的 SHA-256 找到成员：%#v, %v", m, err)
	}
	if other := createMember(t, s); other.MemberToken == session.MemberToken || other.Member.ID == session.Member.ID {
		t.Error("每次都应创建新的成员和 token")
	}
}

func TestUpdateProfile(t *testing.T) {
	s, _ := newService(t)
	id := createMember(t, s).Member.ID

	m, err := s.UpdateProfile(t.Context(), id, Profile{Name: ptr("甲"), Email: ptr(" Alice@Example.COM ")})
	if err != nil {
		t.Fatal(err)
	}
	// md5("alice@example.com")
	if want := (member.Member{ID: id, Name: "甲", AvatarHash: "c160f8cc69a4f0bf2b0362752353d060"}); m != want {
		t.Errorf("修改资料后 = %#v，期望 %#v", m, want)
	}
	if m, _ := s.UpdateProfile(t.Context(), id, Profile{}); m.Name != "甲" || m.AvatarHash == "" {
		t.Errorf("没有传入的字段应保持不变：%#v", m)
	}
	for _, blank := range []string{"", " \t "} {
		if _, err := s.UpdateProfile(t.Context(), id, Profile{Email: ptr("alice@example.com")}); err != nil {
			t.Fatal(err)
		}
		if m, _ := s.UpdateProfile(t.Context(), id, Profile{Email: ptr(blank)}); m.Name != "甲" || m.AvatarHash != "" {
			t.Errorf("邮箱为 %q 时应清除头像：%#v", blank, m)
		}
	}
}

type noMemberBroadcast struct {
	port.Bus
}

func (noMemberBroadcast) PublishMember(context.Context, member.Member) error {
	return porttest.ErrRedisDown
}

func TestUpdateProfileFailsWhenBroadcastFails(t *testing.T) {
	store := porttest.New()
	node := porttest.NewRedis().Node("a")
	s := New(store, store, store, newGateway(store, node, noMemberBroadcast{node}))
	session := createMember(t, s)

	if _, err := s.UpdateProfile(t.Context(), session.Member.ID, Profile{Name: ptr("甲")}); !errors.Is(err, realtime.ErrUnavailable) {
		t.Errorf("广播资料失败时错误 = %v，期望 realtime.ErrUnavailable", err)
	}
	hash := sha256.Sum256([]byte(session.MemberToken))
	if m, err := store.MemberByToken(t.Context(), hash[:]); err != nil || m != (member.Member{ID: session.Member.ID, Name: "甲"}) {
		t.Errorf("广播失败之后存储中的成员 = %#v, %v，期望已经保存新资料", m, err)
	}
}

type passwordChangedStore struct {
	*porttest.Store
	newHash string
}

func (s passwordChangedStore) CreateTicket(ctx context.Context, t port.NewTicket) error {
	if err := s.UpdateRoomAccess(ctx, t.RoomID, port.AccessChange{PasswordHash: &s.newHash}); err != nil {
		return err
	}
	return s.Store.CreateTicket(ctx, t)
}

func TestIssueTicketFailsWhenPasswordChangesMeanwhile(t *testing.T) {
	store := porttest.New()
	changed := passwordChangedStore{Store: store, newHash: hashPassword(t, "新密码")}
	node := porttest.NewRedis().Node("a")
	s := New(store, store, changed, newGateway(store, node, node))
	alice := createMember(t, s).Member
	id := createRoom(t, store, "旧密码")

	if _, err := s.IssueTicket(t.Context(), alice.ID, id, "旧密码"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("密码在校验之后被修改，错误 = %v，期望 ErrWrongPassword", err)
	}
}

type loggedSubscriptions struct {
	port.Bus
	logger *slog.Logger
}

func (b loggedSubscriptions) SubscribeRoom(ctx context.Context, roomID string) error {
	b.logger.InfoContext(ctx, "订阅房间频道", "subscribed", roomID)
	return b.Bus.SubscribeRoom(ctx, roomID)
}

// 进入时传入的可能是别名或大小写不同的房间 ID，网关必须用存储中的 ID 加入，否则同一个房间会被当成两个。
// 这里用别名，因为测试中的房间 ID 只有数字。
func TestEnterRoomJoinsTheStoredRoom(t *testing.T) {
	store := porttest.New()
	node := porttest.NewRedis().Node("a")
	var logs bytes.Buffer
	s := New(store, store, store, newGateway(store, node, loggedSubscriptions{node, slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil)))}))
	alice := createMember(t, s).Member
	id, err := store.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "大厅", Slug: "hall"})
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.IssueTicket(t.Context(), alice.ID, id, "")
	if err != nil {
		t.Fatal(err)
	}

	// 测试中的网关不运行房间，加入在订阅房间频道之后失败，这里只检查订阅时的日志。
	_, _ = s.EnterRoom(t.Context(), "hall", ticket)
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("日志应只有一行 JSON：%v\n%s", err, logs.String())
	}
	if entry["subscribed"] != id || entry["room_id"] != id || entry["member_id"] != alice.ID {
		t.Errorf("进入房间途中的日志 = %v，期望订阅房间 %s，并带着房间 %s 和成员 %s", entry, id, id, alice.ID)
	}
}

func TestEnterRoomRejectsAPIKey(t *testing.T) {
	s, store := newService(t)
	id := createRoom(t, store, "")
	key, k := NewAPIKey()
	if err := store.ResetRoomAPIKey(t.Context(), id, k); err != nil {
		t.Fatal(err)
	}

	if _, err := s.EnterRoom(t.Context(), id, key); !errors.Is(err, ErrBotSession) {
		t.Errorf("用 API Key 建立会话的错误 = %v，期望 ErrBotSession", err)
	}
}
