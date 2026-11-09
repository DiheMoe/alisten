package bootstrap

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres"
	"github.com/dihemoe/alisten/internal/adapter/out/storage"
	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestMediaRedirect(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)
	_, ticket := enterRoom(t, svc, createPersistentRoom(t, svc, "大厅"))
	roomRequest(t, svc, ticket, "POST /api/room/queue", `{"track": {"source": "netease", "id": "186016"}}`)
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	tests := []struct {
		path     string
		status   int
		location string
	}{
		{"/media/netease/186016", http.StatusFound, "https://netease.example/186016.mp3"},
		{"/media/netease/185811", http.StatusNotFound, ""},
		{"/media/qq/0039MnYb0qxYhV", http.StatusBadGateway, ""},
	}
	for _, tt := range tests {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, svc.server.URL+tt.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tt.status || resp.Header.Get("Location") != tt.location {
			t.Errorf("%s = %d %q，期望 %d %q", tt.path, resp.StatusCode, resp.Header.Get("Location"), tt.status, tt.location)
		}
	}
	for _, c := range []struct {
		source, result string
		want           float64
	}{{"netease", "ok", 1}, {"netease", "NOT_FOUND", 1}, {"qq", "PROVIDER_ERROR", 1}} {
		if got := metricValue(t, svc.metrics, "alisten_media_redirects_total", "source", c.source, "result", c.result); got != c.want {
			t.Errorf("alisten_media_redirects_total{%s,%s} = %v，期望 %v", c.source, c.result, got, c.want)
		}
	}
}

func entryStatuses(snapshot map[string]any) map[string]string {
	statuses := map[string]string{}
	add := func(entry any) {
		if e, ok := entry.(map[string]any); ok {
			statuses[e["track"].(map[string]any)["id"].(string)] = e["status"].(string)
		}
	}
	add(snapshot["pending"])
	if playing, ok := snapshot["nowPlaying"].(map[string]any); ok {
		add(playing["entry"])
	}
	for _, e := range snapshot["items"].([]any) {
		add(e)
	}
	return statuses
}

func queuedLinkJobs(t *testing.T, pool *pgxpool.Pool, link string) int {
	t.Helper()
	var n int
	row := pool.QueryRow(t.Context(), "SELECT count(*) FROM river_job WHERE kind = 'prepare' AND queue = 'ytdlp' AND args->>'id' = $1", link)
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRestoredLinksNeedTheirAudio(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	// 服务运行时，对账会接管没有 owner 的房间，可能发生在快照落盘之前，所以先落盘再启动服务。
	if err := runMigrate(newLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	objects, err := loadStorageConfig()
	if err != nil {
		t.Fatal(err)
	}
	dbConfig, err := loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), dbConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	rooms := postgres.New(pool)

	// 上一个 owner 已经把房间落盘：等待队列中有两个已就绪的链接曲目，只有第二个的音频还在对象存储中。
	hall, err := rooms.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	if err != nil {
		t.Fatal(err)
	}
	term, snapshot, err := rooms.TakeRoom(t.Context(), hall)
	if err != nil {
		t.Fatal(err)
	}
	const missing, present = "https://example.com/missing.mp3", "https://example.com/present.mp3"
	for i, link := range []string{missing, present} {
		prepared := port.PreparedTrack{
			Meta:      track.Meta{Title: link, PageURL: link},
			Result:    track.Prepared{Duration: 2 * time.Second, Gain: 1},
			MediaKey:  media.Key(link),
			MediaSize: 3,
		}
		linkTrack := track.Track{Source: track.Link, ID: link}
		if err := rooms.SavePreparedTrack(t.Context(), linkTrack, prepared); err != nil {
			t.Fatal(err)
		}
		snapshot.Waiting = append(snapshot.Waiting, room.Entry{ID: room.EntryID(i + 1), Track: linkTrack, Meta: prepared.Meta, AddedAt: time.Now()})
	}
	snapshot.EntrySeq = 2
	if err := storage.New(objects).Put(t.Context(), media.AudioObject(media.Key(present)), strings.NewReader("mp3"), "audio/mpeg"); err != nil {
		t.Fatal(err)
	}
	if err := rooms.SaveRoom(t.Context(), term, snapshot, nil); err != nil {
		t.Fatal(err)
	}

	svc := startService(t)
	_, ticket := enterRoom(t, svc, hall)
	_, hello := connect(t, svc, hall, ticket)
	want := map[string]string{missing: "preparing", present: "ready"}
	if got := entryStatuses(hello["room"].(map[string]any)); len(got) != len(want) || got[missing] != want[missing] || got[present] != want[present] {
		t.Errorf("接管之后条目的状态 = %v，期望 %v", got, want)
	}
	for deadline := time.Now().Add(messageWait); queuedLinkJobs(t, pool, missing) == 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("音频已不在对象存储中的链接没有重新提交准备任务")
		}
	}
	if n := queuedLinkJobs(t, pool, missing); n != 1 {
		t.Errorf("音频不在的链接有 %d 个准备任务，期望 1 个", n)
	}
	if n := queuedLinkJobs(t, pool, present); n != 0 {
		t.Errorf("音频还在的链接有 %d 个准备任务，期望没有", n)
	}
}

// 准备好的链接曲目进了歌单之后，三处封面地址都由对象存储的公开地址和封面的对象键拼出；换一种大小写写法也能找到它。
func TestPlaylistLinkTracks(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)
	dbConfig, err := loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), dbConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	const link = "https://example.com/Song.mp3"
	cover := track.ObjectKey(media.CoverObject(media.Key(link), "jpg"))
	prepared := port.PreparedTrack{
		Meta:      track.Meta{Title: "Song.mp3", CoverKey: cover, PageURL: link, Duration: 2 * time.Second},
		Result:    track.Prepared{Duration: 2 * time.Second, Gain: 1},
		MediaKey:  media.Key(link),
		MediaSize: 3,
	}
	if err := postgres.New(pool).SavePreparedTrack(t.Context(), track.Track{Source: track.Link, ID: link}, prepared); err != nil {
		t.Fatal(err)
	}
	coverURL := "https://alisten.example/storage/alisten/" + string(cover)

	call := func(request string, status int, resp any) {
		t.Helper()
		line, got, _, body := send(t, svc.server, request)
		if got != status {
			t.Fatalf("%s = %d %s，期望 %d", line, got, body, status)
		}
		if resp != nil {
			if err := json.Unmarshal(body, resp); err != nil {
				t.Fatalf("%s 的响应体 %s：%v", line, body, err)
			}
		}
	}
	var session struct {
		MemberToken string `json:"memberToken"`
	}
	call("POST /api/members\n\n{}", http.StatusCreated, &session)
	auth := "\nAuthorization: Bearer " + session.MemberToken
	call("POST /api/account"+auth+"\n\n"+`{"username": "alice", "password": "correct horse"}`, http.StatusCreated, nil)

	type item struct {
		Track struct {
			ID string `json:"id"`
		} `json:"track"`
		Meta struct {
			CoverURL string `json:"coverUrl"`
		} `json:"meta"`
	}
	var lists struct {
		Items []struct {
			ID       string  `json:"id"`
			CoverURL *string `json:"coverUrl"`
		} `json:"items"`
	}
	call("GET /api/me/playlists"+auth, http.StatusOK, &lists)
	favorites := lists.Items[0].ID

	var added struct {
		Item item `json:"item"`
	}
	call("POST /api/me/playlists/"+favorites+"/items"+auth+"\n\n"+`{"track": {"source": "link", "id": "HTTPS://EXAMPLE.COM/Song.mp3"}}`, http.StatusCreated, &added)
	if added.Item.Track.ID != link || added.Item.Meta.CoverURL != coverURL {
		t.Errorf("加入的曲目 = %+v，期望 ID %s、封面 %s", added.Item, link, coverURL)
	}
	call("GET /api/me/playlists"+auth, http.StatusOK, &lists)
	if got := lists.Items[0].CoverURL; got == nil || *got != coverURL {
		t.Errorf("歌单的封面 = %v，期望 %s", got, coverURL)
	}
	var items struct {
		Items []item `json:"items"`
	}
	call("GET /api/me/playlists/"+favorites+"/items"+auth, http.StatusOK, &items)
	if len(items.Items) != 1 || items.Items[0].Track.ID != link || items.Items[0].Meta.CoverURL != coverURL {
		t.Errorf("歌单里的曲目 = %+v，期望一首 ID %s、封面 %s", items.Items, link, coverURL)
	}

	call("POST /api/me/playlists/"+favorites+"/removals"+auth+"\n\n"+`{"tracks": [{"source": "link", "id": "hTTps://example.COM/Song.mp3"}]}`, http.StatusNoContent, nil)
	call("GET /api/me/playlists/"+favorites+"/items"+auth, http.StatusOK, &items)
	if len(items.Items) != 0 {
		t.Errorf("换一种大小写移出之后还有 %+v，期望没有", items.Items)
	}
}
