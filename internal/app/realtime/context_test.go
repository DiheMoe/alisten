package realtime

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

type requestValue struct{}

type contextStore struct {
	*porttest.Store
	saving chan struct{}
	hold   chan struct{}
	value  any
	err    error
}

func (s *contextStore) SaveRoom(ctx context.Context, term uint32, snap room.Snapshot, plays []room.RecordPlay) error {
	s.saving <- struct{}{}
	<-s.hold
	s.value, s.err = ctx.Value(requestValue{}), ctx.Err()
	return s.Store.SaveRoom(ctx, term, snap, plays)
}

func TestEffectsKeepRequestValues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &contextStore{Store: porttest.New(), saving: make(chan struct{}), hold: make(chan struct{})}
		f := newHostFixture(t, store, store.Store)
		id := f.createRoom(t, room.Persistent)

		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), requestValue{}, "设置播放模式"))
		sent := make(chan error, 1)
		go func() {
			sent <- f.host.send(ctx, id, envelope{input: command("r1", alice, room.SetPlayMode{Mode: room.Random})}, true)
		}()
		<-store.saving
		cancel()
		close(store.hold)
		if err := <-sent; err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if store.value != "设置播放模式" || store.err != nil {
			t.Errorf("落盘时 ctx 的值 = %v、错误 = %v，期望带着请求的值，请求结束之后也没有结束", store.value, store.err)
		}
		checkMessages(t, f.spy.take(), "started 1", "1/1 room.ModeChanged", "reply r1 room.Done")
	})
}

type contextPreparer struct {
	started chan context.Context
}

func (p contextPreparer) Prepare(ctx context.Context, _ track.Track) (track.Meta, track.Prepared, error) {
	p.started <- ctx
	<-ctx.Done()
	return track.Meta{}, track.Prepared{}, ctx.Err()
}

func (contextPreparer) Finished(port.PrepareOutcome) {}

func TestPreparationKeepsRequestValues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		redis := porttest.NewRedis()
		node := redis.Node("a")
		preparer := contextPreparer{started: make(chan context.Context, 1)}
		f := startHost(t, &hostFixture{store: store, redis: redis, node: node, spy: spy{redis.Node("spy")}},
			newHost(store, store, preparer, newSongs().music, cluster(node, "a"), slog.New(slog.DiscardHandler)))
		id := f.createRoom(t, room.Persistent)

		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), requestValue{}, "点歌"))
		if err := f.host.send(ctx, id, envelope{input: command("r1", alice, room.Enqueue{Track: sunny, Meta: sunnyM})}, true); err != nil {
			t.Fatal(err)
		}
		preparing := <-preparer.started
		cancel()
		synctest.Wait()
		if preparing.Value(requestValue{}) != "点歌" || preparing.Err() != nil {
			t.Errorf("准备时 ctx 的值 = %v、错误 = %v，期望带着请求的值，请求结束之后仍在准备", preparing.Value(requestValue{}), preparing.Err())
		}
		if err := f.host.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		if preparing.Err() == nil {
			t.Error("房间停止之后准备仍未停止")
		}
	})
}

func TestFailedRequestsCarryTheRequestIDOfTheOwnerLogs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{Store: porttest.New()}
		var logs logBuffer
		f := startLoggingInstance(t, porttest.NewRedis(), "a", store, store.Store, newSongs(), slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil))))
		id := createRoom(t, store.Store, room.Persistent)
		a := f.join(t, id, alice)
		store.fail.Store(true)

		// 房间被放弃之后要等这次运行结束，下一个请求才会重新接管它并再次落盘；否则下一个请求可能遇到正在结束的运行，不经落盘就失败。
		_, doErr := f.gateway.Do(t.Context(), id, bob, room.SetPlayMode{Mode: room.Random})
		synctest.Wait()
		if !a.Begin() {
			t.Fatal("没有未完成的请求时应能占用名额")
		}
		commandErr := f.gateway.Command(t.Context(), a, "c1", room.SetPlayMode{Mode: room.Random})
		synctest.Wait()
		name := "新名字"
		updateErr := f.gateway.UpdateInfo(t.Context(), id, &name, nil)
		synctest.Wait()

		entries := logs.entries(t, "落盘失败，放弃房间")
		if len(entries) != 3 {
			t.Fatalf("落盘失败的日志有 %d 条，期望 3 条", len(entries))
		}
		for i, err := range []error{doErr, commandErr, updateErr} {
			var failed *RequestError
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &failed) {
				t.Fatalf("第 %d 个请求的错误 = %v，期望带着 RequestID 的 ErrUnavailable", i+1, err)
			}
			if string(failed.ID) != entries[i]["request_id"] || !strings.HasPrefix(string(failed.ID), "a/r") {
				t.Errorf("第 %d 个请求的错误中的 RequestID = %q，owner 日志中的 request_id = %v", i+1, failed.ID, entries[i]["request_id"])
			}
		}
	})
}
