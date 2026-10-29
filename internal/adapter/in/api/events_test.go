package api

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/protocol"
)

func (f *wsFixture) openStream(t *testing.T, credential string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://alisten.test/api/room/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (f *wsFixture) stream(t *testing.T, credential string) *bufio.Reader {
	t.Helper()
	resp := f.openStream(t, credential)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("打开事件流 = %d %s", resp.StatusCode, body)
	}
	return bufio.NewReader(resp.Body)
}

func rejected(t *testing.T, resp *http.Response) response {
	t.Helper()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("事件流应被拒绝，实际打开了")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("拒绝时的 Content-Type = %q", got)
	}
	return response{status: resp.StatusCode, body: strings.TrimSpace(string(body))}
}

// 返回下一个事件的各行，不含结尾的空行。
func readEvent(t *testing.T, r *bufio.Reader) []string {
	t.Helper()
	var lines []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("读事件流：%v，已读到 %q", err, lines)
		}
		if line == "\n" {
			return lines
		}
		lines = append(lines, strings.TrimSuffix(line, "\n"))
	}
}

// data 按 JSON 比较，服务端生成的时间戳换成 1。
func checkEvent(t *testing.T, r *bufio.Reader, want ...string) {
	t.Helper()
	canonical := func(lines []string) string {
		for i, line := range lines {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				lines[i] = "data: " + jsonOf(t, decodeMessage(t, []byte(data)))
			}
		}
		return strings.Join(lines, "\n")
	}
	if got, want := canonical(readEvent(t, r)), canonical(want); got != want {
		t.Errorf("收到\n%s\n期望\n%s", got, want)
	}
}

func checkEnded(t *testing.T, r *bufio.Reader) {
	t.Helper()
	if line, err := r.ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Errorf("close 之后又读到 %q, %v，期望事件流结束", line, err)
	}
}

func TestRoomEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		hall, err := f.admin.CreateRoom(t.Context(), admin.NewRoom{Name: "大厅"})
		if err != nil {
			t.Fatal(err)
		}
		key, err := f.admin.ResetRoomAPIKey(t.Context(), hall)
		if err != nil {
			t.Fatal(err)
		}
		resp := f.openStream(t, key)
		want := map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no"}
		for name, value := range want {
			if got := resp.Header.Get(name); resp.StatusCode != http.StatusOK || got != value {
				t.Errorf("事件流的响应 %d，%s = %q，期望 200 和 %q", resp.StatusCode, name, got, value)
			}
		}
		r := bufio.NewReader(resp.Body)
		checkEvent(t, r, "event: snapshot", `data: {"room":{"term":1,"seq":0,"room":{"id":"`+hall+`","name":"大厅","description":"","kind":"persistent"},"playMode":"sequential","nowPlaying":null,"pending":null,"items":[],"members":{"sessions":[],"onlineCount":0}}}`)

		_, m, ticket := f.enter(t, hall)
		conn := f.join(t, hall, ticket)
		self := `{"avatarHash":"","id":"` + m.ID + `","name":"` + m.Name + `"}`
		checkEvent(t, r, "event: members.updated", "id: 1", `data: {"kind":"event","type":"members.updated","term":1,"seq":1,"data":{"sessions":[`+self+`],"onlineCount":1}}`)
		writeFrame(t, conn, `{"kind":"request","type":"chat.send","id":"c1","data":{"content":"你好"}}`)
		_, frame, err := conn.Read(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got := readEvent(t, r); strings.Join(got, "\n") != "event: chat.message\nid: 2\ndata: "+string(frame) {
			t.Errorf("事件流收到\n%s\n期望 data 与 WebSocket 上的报文相同：\n%s", strings.Join(got, "\n"), frame)
		}

		start := time.Now()
		checkEvent(t, r, ": ping")
		if elapsed := time.Since(start); elapsed != streamHeartbeat {
			t.Errorf("%v 之后收到心跳，期望 %v", elapsed, streamHeartbeat)
		}

		if _, err := f.admin.ResetRoomAPIKey(t.Context(), hall); err != nil {
			t.Fatal(err)
		}
		checkEvent(t, r, "event: close", `data: {"code":4401,"message":"凭证已失效：房间设置或修改了密码，或者 API Key 已重置"}`)
		checkEnded(t, r)
	})
}

func TestRoomEventsCloseWithTheRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		deleted, err := f.admin.CreateRoom(t.Context(), admin.NewRoom{Name: "删除"})
		if err != nil {
			t.Fatal(err)
		}
		_, _, deletedTicket := f.enter(t, deleted)
		_, _, ephemeralTicket := f.enter(t, "")
		gone, restarted := f.stream(t, deletedTicket), f.stream(t, ephemeralTicket)
		readEvent(t, gone)
		readEvent(t, restarted)

		if err := f.admin.DeleteRoom(t.Context(), deleted); err != nil {
			t.Fatal(err)
		}
		checkEvent(t, gone, "event: close", `data: {"code":4404,"message":"房间已关闭或被删除"}`)
		checkEnded(t, gone)
		if err := f.gateway.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		checkEvent(t, restarted, "event: close", `data: {"code":1012,"message":"服务端实例退出或订阅中断，请重新连接"}`)
		checkEnded(t, restarted)
	})
}

func TestRoomEventsLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		hall, err := f.admin.CreateRoom(t.Context(), admin.NewRoom{Name: "大厅"})
		if err != nil {
			t.Fatal(err)
		}
		key, err := f.admin.ResetRoomAPIKey(t.Context(), hall)
		if err != nil {
			t.Fatal(err)
		}
		_, _, ticket := f.enter(t, hall)
		oldest := f.stream(t, key)
		readEvent(t, oldest)
		for range 4 {
			f.stream(t, key)
		}
		want := failure(protocol.ErrorCodeRateLimited, "这个房间的事件流超过 5 条，请先关闭不用的事件流")
		if got := rejected(t, f.openStream(t, ticket)); got != want {
			t.Errorf("名额用完时用别的凭证打开 = %#v，期望 %#v", got, want)
		}
		replacing := f.stream(t, key)
		checkEvent(t, oldest, "event: close", `data: {"code":4409,"message":"同一凭证打开了新的事件流"}`)
		checkEnded(t, oldest)
		if got := readEvent(t, replacing); got[0] != "event: snapshot" {
			t.Errorf("顶替旧事件流的事件流收到 %q，期望从快照开始", got)
		}
	})
}

// 每条事件流用自己的票据，名额用完时不会顶替别的事件流。
func (f *wsFixture) fill(t *testing.T, roomID string) {
	t.Helper()
	for range 5 {
		_, _, ticket := f.enter(t, roomID)
		f.stream(t, ticket)
	}
}

// 接管房间时重置房间的 API Key 但不发通知，就像通知在事件流生效之前就已经发出。
type keyResettingSnapshots struct {
	*porttest.Store
}

func (s keyResettingSnapshots) TakeRoom(ctx context.Context, id string) (uint32, room.Snapshot, error) {
	_, k := identity.NewAPIKey()
	if err := s.ResetRoomAPIKey(ctx, id, k); err != nil {
		return 0, room.Snapshot{}, err
	}
	return s.Store.TakeRoom(ctx, id)
}

func TestRoomEventsRejectTicketRevokedWhileOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, keyResettingSnapshots{store}, store)
		hall, err := f.admin.CreateRoom(t.Context(), admin.NewRoom{Name: "大厅"})
		if err != nil {
			t.Fatal(err)
		}
		key, err := f.admin.ResetRoomAPIKey(t.Context(), hall)
		if err != nil {
			t.Fatal(err)
		}
		want := failure(protocol.ErrorCodeUnauthorized, "票据无效")
		if got := rejected(t, f.openStream(t, key)); got != want {
			t.Errorf("事件流生效之前 Key 已经重置时 = %#v，期望 %#v", got, want)
		}
		f.fill(t, hall)
	})
}

// 校验票据一直没有结果。
type stallingTickets struct {
	*porttest.Store
}

func (stallingTickets) TicketHolder(ctx context.Context, _ []byte) (port.TicketHolder, error) {
	<-ctx.Done()
	return port.TicketHolder{}, ctx.Err()
}

func TestRoomEventsOpeningTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixtureWithTickets(t, store, stallingTickets{store}, store, store)
		// 期限之后客户端也放弃：服务端没有期限时，这里不会一直等下去。
		ctx, cancel := context.WithTimeout(t.Context(), 2*helloTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://alisten.test/api/room/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer 票据")
		start := time.Now()
		resp, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		want := failure(protocol.ErrorCodeUnavailable, "服务器没有及时响应，请稍后再试。")
		if got := rejected(t, resp); got != want {
			t.Errorf("打开事件流超过期限时 = %#v，期望 %#v", got, want)
		}
		if elapsed := time.Since(start); elapsed != helloTimeout {
			t.Errorf("%v 之后才回复，期望 %v", elapsed, helloTimeout)
		}
	})
}

// 第二次查找票据，即事件流生效之后的确认，发生 panic。
type panickingRecheck struct {
	*porttest.Store
	lookups atomic.Int32
}

func (s *panickingRecheck) TicketHolder(ctx context.Context, hash []byte) (port.TicketHolder, error) {
	if s.lookups.Add(1) == 2 {
		panic("票据存储坏了")
	}
	return s.Store.TicketHolder(ctx, hash)
}

func TestRoomEventsReleaseSlotWhenRecheckPanics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixtureWithTickets(t, store, &panickingRecheck{Store: store}, store, store)
		hall, err := f.admin.CreateRoom(t.Context(), admin.NewRoom{Name: "大厅"})
		if err != nil {
			t.Fatal(err)
		}
		key, err := f.admin.ResetRoomAPIKey(t.Context(), hall)
		if err != nil {
			t.Fatal(err)
		}
		want := failure(protocol.ErrorCodeInternal, "服务器出错了，请稍后再试。")
		if got := rejected(t, f.openStream(t, key)); got != want {
			t.Errorf("确认票据时 panic = %#v，期望 %#v", got, want)
		}
		if !strings.Contains(f.logs.String(), "票据存储坏了") {
			t.Errorf("日志中没有这次 panic：\n%s", f.logs)
		}
		f.logs.Reset()
		f.fill(t, hall)
	})
}

func TestRoomEventsEndWhenClientStopsReading(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		hall, err := f.admin.CreateRoom(t.Context(), admin.NewRoom{Name: "大厅"})
		if err != nil {
			t.Fatal(err)
		}
		_, m, ticket := f.enter(t, hall)
		f.stream(t, ticket)
		// 内存连接上的写入要等对方读取：客户端不读，这个事件就一直写不出去。
		if _, err := f.gateway.Do(t.Context(), hall, m, room.Chat{Text: "你好"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(streamWriteTimeout)
		synctest.Wait()
		f.fill(t, hall)
	})
}

func TestRoomEventsNeedRoomCredential(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		session, err := f.identity.CreateMember(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want := failure(protocol.ErrorCodeUnauthorized, "票据无效")
		for _, credential := range []string{"", "不存在", session.MemberToken} {
			if got := rejected(t, f.openStream(t, credential)); got != want {
				t.Errorf("凭证 %q 时 = %#v，期望 %#v", credential, got, want)
			}
		}
	})
}
