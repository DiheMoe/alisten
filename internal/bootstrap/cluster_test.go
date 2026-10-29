package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/adapter/out/redis"
)

// cut 之后断开全部连接，也不再接受新连接，模拟实例与 Redis 之间的网络中断。
type redisProxy struct {
	listener net.Listener
	mu       sync.Mutex
	conns    []net.Conn
	cut      bool
}

func startRedisProxy(t *testing.T, target string) (*redisProxy, string) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &redisProxy{listener: listener}
	t.Cleanup(p.Cut)
	upstream := u.Host
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", upstream)
			if err != nil {
				_ = client.Close()
				continue
			}
			p.mu.Lock()
			if p.cut {
				_ = client.Close()
				_ = server.Close()
			} else {
				p.conns = append(p.conns, client, server)
				go func() { _, _ = io.Copy(server, client); _ = server.Close() }()
				go func() { _, _ = io.Copy(client, server); _ = client.Close() }()
			}
			p.mu.Unlock()
		}
	}()
	u.Host = listener.Addr().String()
	return p, u.String()
}

func (p *redisProxy) Cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cut = true
	_ = p.listener.Close()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

func enterRoom(t *testing.T, svc service, roomID string) (string, string) {
	t.Helper()
	var session struct{ MemberToken string }
	_, _, _, body := send(t, svc.server, "POST /api/members\n\n{}")
	if err := json.Unmarshal(body, &session); err != nil {
		t.Fatal(err)
	}
	line, status, _, body := send(t, svc.server, "POST /api/rooms/"+roomID+"/tickets\nAuthorization: Bearer "+session.MemberToken+"\n\n{}")
	var ticket struct{ Ticket string }
	if err := json.Unmarshal(body, &ticket); err != nil || status != http.StatusCreated {
		t.Fatalf("%s = %d %s", line, status, body)
	}
	return session.MemberToken, ticket.Ticket
}

func createPersistentRoom(t *testing.T, svc service, name string) string {
	t.Helper()
	line, status, _, body := send(t, svc.server, "POST /api/admin/rooms\nAuthorization: Bearer admin-secret\n\n{\"name\": \""+name+"\", \"description\": \"\"}")
	var created struct{ ID string }
	if err := json.Unmarshal(body, &created); err != nil || status != http.StatusCreated {
		t.Fatalf("%s = %d %s", line, status, body)
	}
	return created.ID
}

// 房间没有 owner 时由 svc 接管。
func roomRequest(t *testing.T, svc service, ticket, request, data string) {
	t.Helper()
	line, status, _, body := send(t, svc.server, request+"\nAuthorization: Bearer "+ticket+"\n\n"+data)
	if status/100 != 2 {
		t.Fatalf("%s = %d %s", line, status, body)
	}
}

type wsClient struct {
	conn *websocket.Conn
}

func connect(t *testing.T, svc service, roomID, ticket string) (wsClient, map[string]any) {
	t.Helper()
	c := wsClient{conn: dial(t, svc.server, "")}
	c.write(t, `{"kind":"request","type":"hello","id":"h1","data":{"roomId":"`+roomID+`","ticket":"`+ticket+`","protocolVersion":"`+protocolVersion(t)+`"}}`)
	hello := c.read(t)
	if hello["error"] != nil {
		t.Fatalf("握手失败：%v", hello)
	}
	return c, hello["data"].(map[string]any)
}

func (c wsClient) write(t *testing.T, frame string) {
	t.Helper()
	if err := c.conn.Write(t.Context(), websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
}

func (c wsClient) read(t *testing.T) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_, frame, err := c.conn.Read(ctx)
	if err != nil {
		t.Fatalf("读消息：%v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(frame, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (c wsClient) awaitTerm(t *testing.T, term int) {
	t.Helper()
	for {
		if m := c.read(t); m["type"] == "members.updated" && m["term"] == float64(term) {
			return
		}
	}
}

func newObserver(t *testing.T, redisURL string) *redis.Leases {
	t.Helper()
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := goredis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	return redis.NewLeases(rdb, "observer")
}

func owner(t *testing.T, observer *redis.Leases, roomID string) string {
	t.Helper()
	owners, err := observer.Owners(t.Context(), []string{roomID})
	if err != nil {
		t.Fatal(err)
	}
	return owners[0]
}

// 各实例的对账可能先于按需接管取得新房间，所以测试按实际的 owner 安排其余的实例。
func TestInstancesShareRooms(t *testing.T) {
	databaseURL, redisURL := startPostgres(t)(t), startValkey(t)(t)
	contractEnv(t, databaseURL, redisURL)
	var services []service
	proxies := map[string]*redisProxy{}
	for range 3 {
		proxy, proxied := startRedisProxy(t, redisURL)
		connectionEnv(t, proxied)
		svc := startService(t)
		services = append(services, svc)
		proxies[svc.instance] = proxy
	}
	observer := newObserver(t, redisURL)
	byInstance := func(id string) (service, []service) {
		var owner service
		var others []service
		for _, svc := range services {
			if svc.instance == id {
				owner = svc
			} else {
				others = append(others, svc)
			}
		}
		if owner.server == nil {
			t.Fatalf("实例 %q 不在这次测试中", id)
		}
		return owner, others
	}

	hall := createPersistentRoom(t, services[0], "大厅")
	_, botTicket := enterRoom(t, services[0], hall)
	roomRequest(t, services[0], botTicket, "GET /api/room", "")
	first, others := byInstance(owner(t, observer, hall))
	_, ticket := enterRoom(t, others[0], hall)
	client, hello := connect(t, others[0], hall, ticket)
	if room := hello["room"].(map[string]any); room["term"] != float64(1) || room["members"].(map[string]any)["onlineCount"] != float64(1) {
		t.Errorf("握手的快照 = %v，期望任期 1、在线 1 人", room)
	}
	client.write(t, `{"kind":"request","type":"chat.send","id":"c1","data":{"content":"你好"}}`)
	if m := client.read(t); m["type"] != "chat.message" || m["term"] != float64(1) {
		t.Errorf("收到 %v，期望任期 1 的聊天消息", m)
	}
	if m := client.read(t); m["type"] != "chat.send" || m["error"] != nil {
		t.Errorf("收到 %v，期望聊天的成功回复", m)
	}
	if first.public.host.RoomsOwned() != 1 || others[0].public.host.RoomsOwned() != 0 {
		t.Errorf("owner 运行 %d 个房间、客户端所在的实例运行 %d 个，期望房间一直在 owner", first.public.host.RoomsOwned(), others[0].public.host.RoomsOwned())
	}

	cut := time.Now()
	proxies[first.instance].Cut()
	for got := owner(t, observer, hall); got == "" || got == first.instance; got = owner(t, observer, hall) {
		if time.Since(cut) > 25*time.Second {
			t.Fatalf("owner 断开 %v 之后仍没有实例接管大厅", time.Since(cut))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if takeover := time.Since(cut); takeover < 5*time.Second || takeover > 21*time.Second {
		t.Errorf("owner 断开 %v 之后被接管，期望在租约过期之后、对账的间隔之内", takeover)
	}
	client.awaitTerm(t, 2)

	lounge := createPersistentRoom(t, others[0], "二厅")
	_, loungeBot := enterRoom(t, others[0], lounge)
	roomRequest(t, others[0], loungeBot, "GET /api/room", "")
	var guests []wsClient
	for _, svc := range others {
		_, ticket := enterRoom(t, svc, lounge)
		guest, _ := connect(t, svc, lounge, ticket)
		guests = append(guests, guest)
	}
	leaving, _ := byInstance(owner(t, observer, lounge))
	released := time.Now()
	if err := leaving.public.host.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, guest := range guests {
		guest.awaitTerm(t, 2)
	}
	if elapsed := time.Since(released); elapsed > time.Second {
		t.Errorf("二厅释放 %v 之后才被接管，期望随即接管", elapsed)
	}
	if got := owner(t, observer, lounge); got == "" || got == leaving.instance || got == first.instance {
		t.Errorf("二厅的 owner = %q，期望另一个有会话的实例", got)
	}
}

func TestPreparedOutcomeReachesOwnerOnAnotherInstance(t *testing.T) {
	databaseURL, redisURL := startPostgres(t)(t), startValkey(t)(t)
	contractEnv(t, databaseURL, redisURL)
	services := []service{startService(t), startService(t)}
	observer := newObserver(t, redisURL)
	hall := createPersistentRoom(t, services[0], "大厅")
	_, ticket := enterRoom(t, services[0], hall)
	client, _ := connect(t, services[0], hall, ticket)
	id := owner(t, observer, hall)
	for _, svc := range services {
		if svc.instance == id {
			if err := svc.public.jobs.Stop(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}

	client.write(t, `{"kind":"request","type":"queue.add","id":"a1","data":{"track":{"source":"netease","id":"186016"}}}`)
	added := time.Now()
	for {
		m := client.read(t)
		if m["type"] != "playback.updated" {
			continue
		}
		playing := m["data"].(map[string]any)["nowPlaying"].(map[string]any)
		if playing["durationMs"] != float64(269500) {
			t.Fatalf("开始播放的歌 = %v，期望准备好的晴天", playing)
		}
		break
	}
	if elapsed := time.Since(added); elapsed > messageWait {
		t.Errorf("点歌 %v 之后才开始播放，期望准备结果经 tracks.prepared 送达", elapsed)
	}
}
