package lobby

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/member"
)

func newService(t *testing.T, store *porttest.Store) (*Service, *realtime.Gateway) {
	t.Helper()
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
	return New(store, node), gateway
}

func TestOnlineCount(t *testing.T) {
	store := porttest.New()
	s, gateway := newService(t, store)
	busy, err := s.CreateRoom(t.Context(), NewRoom{Name: "热闹"})
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := s.CreateRoom(t.Context(), NewRoom{Name: "冷清"})
	if err != nil {
		t.Fatal(err)
	}
	// 同一个成员的两个会话算两个在线。
	alice := member.Member{ID: "00000000-0000-7000-8000-00000000000a", Name: "alice"}
	for range 2 {
		session, err := gateway.Join(t.Context(), busy, alice)
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
		online[r.Info.ID] = r.Online
	}
	if online[busy] != 2 || online[quiet] != 0 {
		t.Errorf("在线人数 = %v，期望 %s 为 2，%s 为 0", online, busy, quiet)
	}
	if r, err := s.FindRoom(t.Context(), busy); err != nil || r.Online != 2 {
		t.Errorf("FindRoom = %#v, %v，期望在线人数 2", r, err)
	}
}

type unreadableCounts struct{}

func (unreadableCounts) OnlineCounts(context.Context, []string) ([]int, error) {
	return nil, porttest.ErrRedisDown
}

func TestUnreadableOnlineCountsAreUnavailable(t *testing.T) {
	s := New(porttest.New(), unreadableCounts{})
	id, err := s.CreateRoom(t.Context(), NewRoom{Name: "大厅"})
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
