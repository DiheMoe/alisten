package lobby

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/app/site"
	"github.com/dihemoe/alisten/internal/domain/member"
)

const clientIP = "203.0.113.7"

func newService(t *testing.T, store *porttest.Store) (*Service, *realtime.Gateway) {
	t.Helper()
	node := porttest.NewRedis().Node("a")
	host, gateway := realtime.New(store, store, store, store, nil, porttest.NewPreparer(), realtime.Cluster{Instance: "a", Leases: node, Liveness: node, Bus: node}, slog.New(slog.DiscardHandler))
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
	return New(store, site.New(store), node, porttest.Quota{}), gateway
}

func TestOnlineCount(t *testing.T) {
	store := porttest.New()
	s, gateway := newService(t, store)
	busy, err := s.CreateRoom(t.Context(), clientIP, NewRoom{Name: "热闹"})
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := s.CreateRoom(t.Context(), clientIP, NewRoom{Name: "冷清"})
	if err != nil {
		t.Fatal(err)
	}
	// 同一个成员的两个会话只算一个人。
	alice := member.Member{ID: "00000000-0000-7000-8000-00000000000a", Name: "alice"}
	bob := member.Member{ID: "00000000-0000-7000-8000-00000000000b", Name: "bob"}
	for _, m := range []member.Member{alice, alice, bob} {
		session, err := gateway.Join(t.Context(), busy, m, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { gateway.Leave(session) })
	}

	rooms, err := s.ListRooms(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	online := map[string]int{}
	for _, r := range rooms {
		online[r.Info.ID] = r.OnlineCount
	}
	if online[busy] != 2 || online[quiet] != 0 {
		t.Errorf("在线人数 = %v，期望 %s 为 2，%s 为 0", online, busy, quiet)
	}
	if r, err := s.FindRoom(t.Context(), busy); err != nil || r.OnlineCount != 2 {
		t.Errorf("FindRoom = %#v, %v，期望在线人数 2", r, err)
	}
}

type unreadableCounts struct{}

func (unreadableCounts) OnlineCounts(context.Context, []string) ([]int, error) {
	return nil, porttest.ErrRedisDown
}

func TestUnreadableOnlineCountsAreUnavailable(t *testing.T) {
	store := porttest.New()
	s := New(store, site.New(store), unreadableCounts{}, porttest.Quota{})
	id, err := s.CreateRoom(t.Context(), clientIP, NewRoom{Name: "大厅"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListRooms(t.Context()); !errors.Is(err, realtime.ErrUnavailable) {
		t.Errorf("ListRooms 的错误 = %v，期望 ErrUnavailable", err)
	}
	if _, err := s.FindRoom(t.Context(), id); !errors.Is(err, realtime.ErrUnavailable) {
		t.Errorf("FindRoom 的错误 = %v，期望 ErrUnavailable", err)
	}
}

func TestCreateRoomRateLimitedByClientIP(t *testing.T) {
	store := porttest.New()
	s := New(store, site.New(store), porttest.NewRedis().Node("a"), porttest.Quota{Exhausted: map[string]bool{"room:ip:" + clientIP: true}})

	if _, err := s.CreateRoom(t.Context(), clientIP, NewRoom{Name: "临时"}); !errors.Is(err, ErrTooManyRooms) {
		t.Errorf("额度用完时的错误 = %v，期望 ErrTooManyRooms", err)
	}
	if _, err := s.CreateRoom(t.Context(), "203.0.113.8", NewRoom{Name: "临时"}); err != nil {
		t.Errorf("另一个 IP 不受影响：%v", err)
	}
}

var errSiteDown = errors.New("数据库不可达")

type unreadableSite struct{ port.Site }

func (unreadableSite) Settings(context.Context) (port.SiteSettings, error) {
	return port.SiteSettings{}, errSiteDown
}

type countingQuota struct{ taken int }

func (q *countingQuota) Take(context.Context, string, int, time.Duration) (time.Duration, error) {
	q.taken++
	return 0, nil
}

func TestCreateRoomKeepsTheQuotaWhenSettingsAreUnreadable(t *testing.T) {
	store := porttest.New()
	quota := &countingQuota{}
	s := New(store, site.New(unreadableSite{store}), porttest.NewRedis().Node("a"), quota)
	if _, err := s.CreateRoom(t.Context(), clientIP, NewRoom{Name: "临时"}); !errors.Is(err, errSiteDown) {
		t.Fatalf("读不出全站设置时建房 = %v，期望返回读取的错误", err)
	}
	if quota.taken != 0 {
		t.Errorf("读不出全站设置时扣了 %d 次建房额度，期望不扣", quota.taken)
	}
}

func TestEphemeralRoomCapacityComesFromSiteSettings(t *testing.T) {
	store := porttest.New()
	siteService := site.New(store)
	s := New(store, siteService, porttest.NewRedis().Node("a"), porttest.Quota{})
	create := func(name string) string {
		t.Helper()
		id, err := s.CreateRoom(t.Context(), clientIP, NewRoom{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	capacity := func(id string) int {
		t.Helper()
		r, err := store.FindRoom(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return r.Info.Capacity
	}

	earlier := create("没设置过")
	if got := capacity(earlier); got != 16 {
		t.Errorf("没有设置过临时房间最多人数时，新房间的人数上限 = %d，期望 16", got)
	}
	if err := siteService.SetEphemeralRoomCapacity(t.Context(), 40); err != nil {
		t.Fatal(err)
	}
	if got := capacity(create("设置过")); got != 40 {
		t.Errorf("临时房间最多人数设成 40 之后，新房间的人数上限 = %d，期望 40", got)
	}
	if got := capacity(earlier); got != 16 {
		t.Errorf("设置之前建的临时房间的人数上限 = %d，期望仍是 16", got)
	}
}
