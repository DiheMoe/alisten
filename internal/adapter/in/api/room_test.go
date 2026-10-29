package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
	"github.com/dihemoe/alisten/protocol"
)

type roomFixture struct {
	server  *httptest.Server
	store   *porttest.Store
	gateway *realtime.Gateway
}

func newRoomFixture(t *testing.T) roomFixture {
	t.Helper()
	store := porttest.New()
	music := newCatalog(store, 10)
	node := porttest.NewRedis().Node("a")
	host, gateway := realtime.New(store, store, music, porttest.NewPreparer(), realtime.Cluster{Instance: "a", Leases: node, Liveness: node, Bus: node}, slog.New(slog.DiscardHandler))
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
	server, _ := start(t, Services{
		Identity:   identity.New(store, store, store, gateway),
		Lobby:      l,
		Admin:      admin.New("secret", l, store, store, store, porttest.NewNetEaseQRLogin(), porttest.NewQQQRLogin(), porttest.NewBilibiliQRLogin(), porttest.NewYouTubeCookies(), gateway),
		Gateway:    gateway,
		Catalog:    music,
		StorageURL: "https://alisten.example/storage/alisten",
	})
	return roomFixture{server: server, store: store, gateway: gateway}
}

func decode[T any](t *testing.T, r response) T {
	t.Helper()
	var v T
	if r.status != http.StatusOK && r.status != http.StatusCreated {
		t.Fatalf("请求失败：%#v", r)
	}
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f roomFixture) enter(t *testing.T) (string, member.Member, string) {
	t.Helper()
	session := decode[protocol.CreateMemberResponse](t, send(t, f.server, http.MethodPost, "/api/members", "{}"))
	created := decode[protocol.CreateRoomResponse](t, send(t, f.server, http.MethodPost, "/api/rooms", `{"name":"临时","description":""}`))
	ticket := decode[protocol.CreateTicketResponse](t, send(t, f.server, http.MethodPost, "/api/rooms/"+created.ID+"/tickets", "{}", "Authorization", "Bearer "+session.MemberToken))
	m := member.Member{ID: session.Member.ID, Name: session.Member.Name}
	return created.ID, m, ticket.Ticket
}

var pathParam = regexp.MustCompile(`\{[A-Za-z]+\}`)

func TestRoomRoutesNeedTicket(t *testing.T) {
	f := newRoomFixture(t)
	want := failure(protocol.ErrorCodeUnauthorized, "票据无效")
	for _, route := range protocol.RoomRoutes {
		method, path, _ := strings.Cut(route.Pattern, " ")
		path = pathParam.ReplaceAllString(path, "1")
		for _, header := range [][]string{nil, {"Authorization", "Bearer 不存在"}} {
			if got := send(t, f.server, method, path, "不是 JSON", header...); got != want {
				t.Errorf("%s %s，请求头 %q 时 = %#v，期望 %#v", method, path, header, got, want)
			}
		}
	}
}

type countingServer struct {
	server  *httptest.Server
	handler http.Handler
	metrics *prometheus.Registry
	logs    *bytes.Buffer
}

func startCounting(t *testing.T, s Services) countingServer {
	t.Helper()
	metrics := prometheus.NewRegistry()
	var logs bytes.Buffer
	handler := SameOrigin([]string{allowedOrigin, "http://localhost:9876"}, New(s, metrics, noop.NewTracerProvider(), slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil)))))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return countingServer{server: server, handler: handler, metrics: metrics, logs: &logs}
}

func commandCounts(t *testing.T, metrics prometheus.Gatherer) map[string]float64 {
	t.Helper()
	families, err := metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "alisten_room_commands_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			counts[labels["type"]+"/"+labels["result"]] = m.GetCounter().GetValue()
		}
	}
	return counts
}

func (c countingServer) ticket(t *testing.T) string {
	t.Helper()
	created := decode[protocol.CreateRoomResponse](t, send(t, c.server, http.MethodPost, "/api/rooms", `{"name":"临时","description":""}`))
	return decode[protocol.CreateTicketResponse](t, send(t, c.server, http.MethodPost, "/api/rooms/"+created.ID+"/tickets", "{}", "Authorization", "Bearer "+memberToken(t, c.server))).Ticket
}

var searchPath = "/api/room/search/tracks?" + url.Values{"source": {"netease"}, "keyword": {"晴天"}, "page": {"1"}, "pageSize": {"10"}}.Encode()

func TestPanickingRoomRequestIsCountedAsInternal(t *testing.T) {
	store := porttest.New()
	directory := &panickingDirectory{Store: store}
	s := services(store, store, "")
	s.Catalog = newCatalog(directory, 10)
	c := startCounting(t, s)
	ticket := c.ticket(t)
	if got := send(t, c.server, http.MethodGet, searchPath, "", "Authorization", "Bearer "+ticket); got.status != http.StatusOK {
		t.Fatalf("panic 之前的搜索 = %#v，期望成功", got)
	}

	directory.armed.Store(true)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, searchPath, nil)
	req.Header.Set("Authorization", "Bearer "+ticket)
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	got := response{status: rec.Code, body: strings.TrimSpace(rec.Body.String())}
	if want := failure(protocol.ErrorCodeInternal, "服务器出错了，请稍后再试。"); got != want || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("panic 的请求 = %#v、Content-Type = %q，期望 %#v、application/json", got, rec.Header().Get("Content-Type"), want)
	}
	var entry map[string]any
	if err := json.Unmarshal(c.logs.Bytes(), &entry); err != nil {
		t.Fatalf("日志应只有一行 JSON：%v\n%s", err, c.logs)
	}
	stack, _ := entry["stack"].(string)
	if entry["level"] != "ERROR" || entry["msg"] != "处理 HTTP 请求时发生 panic" || entry["panic"] != "房间目录坏了" || entry["method"] != http.MethodGet || entry["path"] != "/api/room/search/tracks" || !strings.Contains(stack, "panickingDirectory") {
		t.Errorf("日志 = %v，期望记录 panic 的值、请求的方法和路径，stack 字段是 panic 处的调用栈", entry)
	}
	if entry["room_id"] == nil || entry["member_id"] == nil {
		t.Errorf("日志 = %v，期望带着票据所属的房间和成员", entry)
	}
	want := map[string]float64{"search.tracks/ok": 1, "search.tracks/INTERNAL": 1}
	if got := commandCounts(t, c.metrics); !maps.Equal(got, want) {
		t.Errorf("alisten_room_commands_total = %v，期望 %v", got, want)
	}
}

type disconnectingDirectory struct {
	*porttest.Store
	armed      atomic.Bool
	disconnect context.CancelFunc
}

func (d *disconnectingDirectory) FindRoom(ctx context.Context, ref string) (port.RoomRecord, error) {
	if !d.armed.Load() {
		return d.Store.FindRoom(ctx, ref)
	}
	d.disconnect()
	<-ctx.Done()
	return port.RoomRecord{}, ctx.Err()
}

func TestRoomRequestOfDisconnectedClientIsNotCounted(t *testing.T) {
	store := porttest.New()
	ctx, disconnect := context.WithCancel(t.Context())
	directory := &disconnectingDirectory{Store: store, disconnect: disconnect}
	s := services(store, store, "")
	s.Catalog = newCatalog(directory, 10)
	c := startCounting(t, s)
	ticket := c.ticket(t)
	if got := send(t, c.server, http.MethodGet, searchPath, "", "Authorization", "Bearer "+ticket); got.status != http.StatusOK {
		t.Fatalf("断开之前的搜索 = %#v，期望成功", got)
	}

	directory.armed.Store(true)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, searchPath, nil)
	req.Header.Set("Authorization", "Bearer "+ticket)
	c.handler.ServeHTTP(httptest.NewRecorder(), req)
	want := map[string]float64{"search.tracks/ok": 1}
	if got := commandCounts(t, c.metrics); !maps.Equal(got, want) {
		t.Errorf("alisten_room_commands_total = %v，期望 %v", got, want)
	}
}

func TestRoomListingShowsOnlineCount(t *testing.T) {
	f := newRoomFixture(t)
	id, m, _ := f.enter(t)
	session, err := f.gateway.Join(t.Context(), id, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.gateway.Leave(session) })

	rooms := decode[protocol.ListRoomsResponse](t, send(t, f.server, http.MethodGet, "/api/rooms", ""))
	if len(rooms.Items) != 1 || rooms.Items[0].OnlineCount != 1 {
		t.Errorf("房间列表 = %+v，期望在线人数 1", rooms.Items)
	}
	if got := decode[protocol.RoomListing](t, send(t, f.server, http.MethodGet, "/api/rooms/"+id, "")); got.OnlineCount != 1 {
		t.Errorf("GET /api/rooms/{ref} = %+v，期望在线人数 1", got)
	}
}

func TestProfileChangeReachesRooms(t *testing.T) {
	f := newRoomFixture(t)
	session := decode[protocol.CreateMemberResponse](t, send(t, f.server, http.MethodPost, "/api/members", "{}"))
	created := decode[protocol.CreateRoomResponse](t, send(t, f.server, http.MethodPost, "/api/rooms", `{"name":"临时","description":""}`))
	m := member.Member{ID: session.Member.ID, Name: session.Member.Name}
	s, err := f.gateway.Join(t.Context(), created.ID, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.gateway.Leave(s) })

	decode[protocol.UpdateMeResponse](t, send(t, f.server, http.MethodPatch, "/api/me", `{"name":"甲"}`, "Authorization", "Bearer "+session.MemberToken))
	// 第一条是握手的结果，第二条是改名之后的在线列表。
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for range 2 {
		msg, _, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := msg.(realtime.Event); ok {
			changed, ok := e.Emit.Event.(room.MembersChanged)
			if !ok || len(changed.Sessions) != 1 || changed.Sessions[0].Member.Name != "甲" {
				t.Errorf("改名之后收到 %+v，期望新的在线列表", e.Emit.Event)
			}
			return
		}
	}
	t.Error("改名之后没有收到在线列表")
}

func TestAdminRoomEndpoints(t *testing.T) {
	f := newRoomFixture(t)
	admin := []string{"Authorization", "Bearer secret"}
	created := decode[protocol.AdminCreateRoomResponse](t, send(t, f.server, http.MethodPost, "/api/admin/rooms", `{"name":"大厅","description":"常驻","slug":"hall"}`, admin...))
	path := "/api/admin/rooms/" + created.ID

	resp, header := exchange(t, f.server, http.MethodPatch, path, `{"name":"新大厅","slug":""}`, admin...)
	checkNoContent(t, "PATCH "+path, resp, header)
	listing := decode[protocol.RoomListing](t, send(t, f.server, http.MethodGet, "/api/rooms/"+created.ID, ""))
	if listing.Name != "新大厅" || listing.Description != "常驻" || listing.Slug != nil {
		t.Errorf("修改之后 = %+v，期望新的名称、原来的描述、没有别名", listing)
	}

	tests := []struct {
		method string
		path   string
		body   string
		header []string
		want   response
	}{
		{http.MethodPatch, path, `{"slug":"Hall"}`, admin, failure(protocol.ErrorCodeInvalidArgument, "slug：格式不正确")},
		{http.MethodPatch, path, `{}`, []string{"Authorization", "Bearer wrong"}, failure(protocol.ErrorCodeUnauthorized, "管理 token 无效")},
		{http.MethodPatch, "/api/admin/rooms/hall", `{}`, admin, failure(protocol.ErrorCodeNotFound, "房间不存在")},
		{http.MethodDelete, "/api/admin/rooms/%00", "", admin, failure(protocol.ErrorCodeNotFound, "房间不存在")},
	}
	for _, tt := range tests {
		if got := send(t, f.server, tt.method, tt.path, tt.body, tt.header...); got != tt.want {
			t.Errorf("%s %s = %#v，期望 %#v", tt.method, tt.path, got, tt.want)
		}
	}

	resp, header = exchange(t, f.server, http.MethodDelete, path, "", admin...)
	checkNoContent(t, "DELETE "+path, resp, header)
	if got := send(t, f.server, http.MethodGet, "/api/rooms/"+created.ID, ""); got != failure(protocol.ErrorCodeNotFound, "房间不存在") {
		t.Errorf("删除之后 GET = %#v", got)
	}
	if _, err := f.store.FindRoom(t.Context(), created.ID); err == nil {
		t.Error("删除之后房间仍在存储中")
	}
}

func TestCreatedResponses(t *testing.T) {
	f := newRoomFixture(t)
	admin := []string{"Authorization", "Bearer secret"}

	resp, header := exchange(t, f.server, http.MethodPost, "/api/members", "{}")
	checkCreated(t, "POST /api/members", resp, header, "")
	session := decode[protocol.CreateMemberResponse](t, resp)
	resp, header = exchange(t, f.server, http.MethodPost, "/api/rooms", `{"name":"临时","description":""}`)
	open := decode[protocol.CreateRoomResponse](t, resp)
	checkCreated(t, "POST /api/rooms", resp, header, "/api/rooms/"+open.ID)
	resp, header = exchange(t, f.server, http.MethodPost, "/api/rooms/"+open.ID+"/tickets", "{}", "Authorization", "Bearer "+session.MemberToken)
	checkCreated(t, "POST /api/rooms/{id}/tickets", resp, header, "")
	resp, header = exchange(t, f.server, http.MethodPost, "/api/admin/rooms", `{"name":"大厅","description":""}`, admin...)
	hall := decode[protocol.AdminCreateRoomResponse](t, resp)
	checkCreated(t, "POST /api/admin/rooms", resp, header, "/api/rooms/"+hall.ID)
	resp, header = exchange(t, f.server, http.MethodPost, "/api/admin/rooms/"+hall.ID+"/api-key", "{}", admin...)
	checkCreated(t, "POST /api/admin/rooms/{id}/api-key", resp, header, "/api/admin/rooms/"+hall.ID+"/api-key")
	if key := decode[protocol.APIKey](t, resp); key.Key == "" {
		t.Errorf("生成的 API Key 为空：%#v", resp)
	}
}

func (f *wsFixture) exchange(t *testing.T, method, path, body string, header ...string) (response, http.Header) {
	t.Helper()
	return roundTrip(t, f.client, method, "http://alisten.test"+path, body, header...)
}

func (f *wsFixture) send(t *testing.T, method, path, body string, header ...string) response {
	t.Helper()
	resp, _ := f.exchange(t, method, path, body, header...)
	return resp
}

func TestRoomRoutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		f.preparer.Add(sunnyTrack, sunnyEntry.Meta, sunnyEntry.Prep)
		f.preparer.Add(rain.Track, rain.Meta, track.Prepared{Duration: rain.Meta.Duration, Gain: 1})
		_, _, ticket := f.enter(t, "")
		auth := []string{"Authorization", "Bearer " + ticket}

		if got, want := f.send(t, http.MethodPost, "/api/room/skip-votes", "", auth...), failure(protocol.ErrorCodeNothingPlaying, "现在没有在放的歌。"); got != want {
			t.Errorf("没有歌时投票 = %#v，期望 %#v", got, want)
		}
		resp, header := f.exchange(t, http.MethodPost, "/api/room/queue", `{"track":{"source":"netease","id":"186016"}}`, auth...)
		checkCreated(t, "点歌", resp, header, "/api/room/queue/1")
		// 等这首歌准备好开播。
		synctest.Wait()
		resp, header = f.exchange(t, http.MethodPost, "/api/room/queue", `{"track":{"source":"netease","id":"185811"}}`, auth...)
		checkCreated(t, "再点一首", resp, header, "/api/room/queue/2")
		if added := decode[protocol.QueueAddResponse](t, resp); added.Entry.ID != "2" || added.Entry.Meta.Title != "七里香" {
			t.Errorf("点歌的响应 = %+v", added)
		}
		if got := f.send(t, http.MethodPost, "/api/room/queue/2/likes", "", auth...); got != (response{status: http.StatusOK, body: `{"likes":1}`}) {
			t.Errorf("点赞 = %#v", got)
		}
		resp, header = f.exchange(t, http.MethodDelete, "/api/room/queue/2", "", auth...)
		checkNoContent(t, "删歌", resp, header)
		resp, header = f.exchange(t, http.MethodPatch, "/api/room/settings", `{"playMode":"rotation"}`, auth...)
		checkNoContent(t, "切换播放模式", resp, header)
		resp, header = f.exchange(t, http.MethodPost, "/api/room/messages", `{"content":"你好"}`, auth...)
		checkNoContent(t, "聊天", resp, header)
		if got := f.send(t, http.MethodPost, "/api/room/skip-votes", "{}", auth...); got != (response{status: http.StatusOK, body: `{"required":1,"skipped":true,"votes":1}`}) {
			t.Errorf("投票 = %#v", got)
		}
		snapshot := decode[protocol.RoomSyncResponse](t, f.send(t, http.MethodGet, "/api/room", "", auth...)).Room
		if snapshot.PlayMode != protocol.PlayModeRotation || snapshot.NowPlaying != nil || len(snapshot.Items) != 0 {
			t.Errorf("房间快照 = %+v，期望轮流播放、没有正在播放和等待的歌", snapshot)
		}

		if found := decode[protocol.SearchTracksResponse](t, f.send(t, http.MethodGet, searchPath, "", auth...)); found.Total != 1 || found.Items[0].Track.ID != sunnyTrack.ID {
			t.Errorf("搜索 = %+v", found)
		}
		if tracks := decode[protocol.PlaylistTracksResponse](t, f.send(t, http.MethodGet, "/api/room/playlists/netease/p1/tracks", "", auth...)); tracks.Total != 2 {
			t.Errorf("歌单内的歌曲 = %+v", tracks)
		}
		if recommended := decode[protocol.RecommendListResponse](t, f.send(t, http.MethodGet, "/api/room/recommendations", "", auth...)); len(recommended.Items) != 1 || recommended.Items[0].Track.ID != rain.Track.ID {
			t.Errorf("推荐列表 = %+v", recommended)
		}
		if got, want := f.send(t, http.MethodGet, "/api/room/search/tracks?source=netease&keyword=a&page=one&pageSize=10", "", auth...), failure(protocol.ErrorCodeInvalidArgument, "page：应为整数"); got != want {
			t.Errorf("页码不是整数 = %#v，期望 %#v", got, want)
		}
		if got, want := f.send(t, http.MethodPost, "/api/room/messages?echo=1", `{"content":"你好"}`, auth...), failure(protocol.ErrorCodeInvalidArgument, "echo：未知参数"); got != want {
			t.Errorf("POST 带了查询参数 = %#v，期望 %#v", got, want)
		}
	})
}
