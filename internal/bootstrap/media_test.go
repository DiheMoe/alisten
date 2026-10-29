package bootstrap

import (
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
