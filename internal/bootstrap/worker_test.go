package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dihemoe/alisten/internal/adapter/out/queue"
	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/loudness"
)

type links struct {
	mu   sync.Mutex
	done []string
}

func (l *links) Download(_ context.Context, link string) (port.LinkDownload, func(), error) {
	l.mu.Lock()
	l.done = append(l.done, link)
	l.mu.Unlock()
	d := port.LinkDownload{
		Meta:     track.Meta{Title: "晴天", Artist: "周杰伦"},
		Audio:    porttest.LinkFile(porttest.SilentWAV(2 * time.Second)),
		Cover:    porttest.LinkFile("jpeg"),
		CoverExt: "jpg",
	}
	return d, func() {}, nil
}

func (l *links) downloads() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.done)
}

type runningWorker struct {
	worker mediaWorker
	ops    string
	logs   *bytes.Buffer
	spans  *tracetest.SpanRecorder
	stop   context.CancelFunc
	result func() error
}

// 只准备链接的测试用不到 B 站的音源和转码。
func startWorker(t *testing.T, download port.LinkDownloader) runningWorker {
	t.Helper()
	return startMediaWorker(t, workerSources{links: download})
}

func startMediaWorker(t *testing.T, src workerSources) runningWorker {
	t.Helper()
	cfg, err := loadWorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	spans := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	traceDatabase(cfg.database, tracer)
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg.database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	rdb := goredis.NewClient(cfg.redis)
	t.Cleanup(func() { _ = rdb.Close() })
	traceRedis(rdb, tracer)
	var logs bytes.Buffer
	metrics := prometheus.NewRegistry()
	w, err := newMediaWorker(cfg, pool, rdb, src, metrics, newWorkerMetrics(metrics), rand.Text(), tracer, newLogger(&logs, slog.LevelInfo))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.run(ctx, listener) }()
	running := runningWorker{worker: w, ops: "http://" + listener.Addr().String(), logs: &logs, spans: spans, stop: stop, result: sync.OnceValue(func() error { return <-done })}
	t.Cleanup(func() {
		stop()
		_ = running.result()
	})
	return running
}

func (w runningWorker) readyz(t *testing.T) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, w.ops+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (w runningWorker) waitReady(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(2*readinessInterval + time.Second); w.readyz(t) != http.StatusOK; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("worker 没有就绪：\n%s", w.logs.String())
		}
	}
}

func (c wsClient) readUntil(t *testing.T, match func(map[string]any) bool) map[string]any {
	t.Helper()
	for {
		if m := c.read(t); match(m) {
			return m
		}
	}
}

func playing(m map[string]any) bool {
	if m["type"] != "playback.updated" {
		return false
	}
	return m["data"].(map[string]any)["nowPlaying"] != nil
}

func anonymousGet(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestWorkerPreparesLinks(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	objects, err := loadStorageConfig()
	if err != nil {
		t.Fatal(err)
	}
	storageURL := objects.Endpoint + "/" + objects.Bucket
	t.Setenv("STORAGE_PUBLIC_URL", storageURL)
	svc := startService(t)
	downloads := &links{}
	w := startWorker(t, downloads)
	w.waitReady(t)

	hall := createPersistentRoom(t, svc, "大厅")
	_, ticket := enterRoom(t, svc, hall)
	client, _ := connect(t, svc, hall, ticket)
	const link = "https://example.com/qingtian.mp3"
	key := media.Key(link)
	addLink := `{"kind":"request","type":"queue.add","id":"a1","data":{"track":{"source":"link","id":"` + link + `"}}}`
	client.write(t, addLink)
	now := client.readUntil(t, playing)["data"].(map[string]any)["nowPlaying"].(map[string]any)
	entryMeta := now["entry"].(map[string]any)["meta"].(map[string]any)
	if now["mediaUrl"] != storageURL+"/link/"+key+"/audio.mp3" || now["durationMs"] != float64(2000) ||
		entryMeta["title"] != "晴天" || entryMeta["coverUrl"] != storageURL+"/link/"+key+"/cover.jpg" || entryMeta["pageUrl"] != link {
		t.Fatalf("开始播放的链接 = %v", now)
	}
	if status, body := anonymousGet(t, now["mediaUrl"].(string)); status != http.StatusOK || body != string(porttest.SilentWAV(2*time.Second)) {
		t.Errorf("匿名读取音频 = %d，期望读到上传的音频", status)
	}
	if status, body := anonymousGet(t, entryMeta["coverUrl"].(string)); status != http.StatusOK || body != "jpeg" {
		t.Errorf("匿名读取封面 = %d %q", status, body)
	}

	admin := "Authorization: Bearer admin-secret"
	if line, status, _, body := send(t, svc.server, "GET /api/admin/media\n"+admin); status != http.StatusOK || !strings.Contains(string(body), `"key":"`+key+`"`) {
		t.Errorf("%s = %d %s，期望列出这首链接", line, status, body)
	}
	if line, status, _, body := send(t, svc.server, "DELETE /api/admin/media/"+key+"\n"+admin); status != http.StatusConflict {
		t.Errorf("%s = %d %s，期望正在播放的链接不能删除", line, status, body)
	}

	client.write(t, `{"kind":"request","type":"playback.voteSkip","id":"v1","data":{}}`)
	client.readUntil(t, func(m map[string]any) bool { return m["type"] == "playback.voteSkip" })
	// 切歌之后要等落盘完成，链接曲目才不再被房间引用。
	for deadline := time.Now().Add(messageWait); ; time.Sleep(100 * time.Millisecond) {
		_, status, _, _ := send(t, svc.server, "DELETE /api/admin/media/"+key+"\n"+admin)
		if status == http.StatusNoContent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("切歌之后删除媒体 = %d", status)
		}
	}
	if status, _ := anonymousGet(t, now["mediaUrl"].(string)); status == http.StatusOK {
		t.Error("删除之后仍能读到音频")
	}
	if _, status, _, body := send(t, svc.server, "GET /api/admin/media\n"+admin); status != http.StatusOK || strings.TrimSpace(string(body)) != `{"items":[]}` {
		t.Errorf("删除之后的列表 = %d %s", status, body)
	}

	client.write(t, strings.Replace(addLink, `"a1"`, `"a2"`, 1))
	client.readUntil(t, playing)
	if n := downloads.downloads(); n != 2 {
		t.Errorf("下载了 %d 次，期望删除之后再点会重新下载", n)
	}
	if got := metricValue(t, w.worker.metrics, "alisten_prepare_jobs_total", "source", "link", "result", "ok"); got != 2 {
		t.Errorf("alisten_prepare_jobs_total{link,ok} = %v，期望 2", got)
	}
	for _, name := range []string{"alisten_ready", "alisten_ytdlp_jobs_waiting", "alisten_ytdlp_download_duration_seconds"} {
		metricValue(t, w.worker.metrics, name)
	}

	w.stop()
	if err := w.result(); err != nil || !strings.Contains(w.logs.String(), "已停止执行准备任务") {
		t.Errorf("worker 退出 = %v，日志：\n%s", err, w.logs.String())
	}
}

func TestWorkerPreparesBilibili(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	objects, err := loadStorageConfig()
	if err != nil {
		t.Fatal(err)
	}
	storageURL := objects.Endpoint + "/" + objects.Bucket
	t.Setenv("STORAGE_PUBLIC_URL", storageURL)
	svc := startService(t)
	// serve 和 worker 各自构造音源：serve 的那份照常给出付费视频的元数据，worker 的这份拒绝下载它，与真实的平台一样。
	bilibili := contractBilibili()
	bilibili.RejectVideo("BV1GJ411x7h7", "这个视频需要付费")
	transcoder := porttest.NewTranscoder(porttest.SilentWAV(2 * time.Second))
	w := startMediaWorker(t, workerSources{links: &links{}, bilibili: bilibili, transcoder: transcoder})
	w.waitReady(t)

	line, status, _, body := send(t, svc.server, "POST /api/rooms\n\n{\"name\": \"临时\", \"description\": \"\"}")
	var created struct{ ID string }
	if err := json.Unmarshal(body, &created); err != nil || status != http.StatusCreated {
		t.Fatalf("%s = %d %s", line, status, body)
	}
	_, ticket := enterRoom(t, svc, created.ID)
	client, _ := connect(t, svc, created.ID, ticket)
	client.write(t, `{"kind":"request","type":"queue.add","id":"a1","data":{"track":{"source":"link","id":"https://b23.tv/qingtian"}}}`)
	now := client.readUntil(t, playing)["data"].(map[string]any)["nowPlaying"].(map[string]any)
	entry := now["entry"].(map[string]any)
	meta := entry["meta"].(map[string]any)
	const id = "BV1xx411c7mD/2"
	key := media.Key(id)
	if tr := entry["track"].(map[string]any); tr["source"] != "bilibili" || tr["id"] != id {
		t.Fatalf("开始播放的曲目 = %v，期望 B 站的 %s", tr, id)
	}
	if now["mediaUrl"] != storageURL+"/link/"+key+"/audio.mp3" || now["durationMs"] != float64(2000) ||
		meta["coverUrl"] != storageURL+"/link/"+key+"/cover.jpg" || meta["pageUrl"] != "https://www.bilibili.com/video/BV1xx411c7mD?p=2" || meta["title"] != "晴天（钢琴版）" {
		t.Fatalf("开始播放的 B 站的歌 = %v", now)
	}
	if status, body := anonymousGet(t, now["mediaUrl"].(string)); status != http.StatusOK || body != string(porttest.SilentWAV(2*time.Second)) {
		t.Errorf("匿名读取音频 = %d，期望读到转出的 mp3", status)
	}
	if status, body := anonymousGet(t, meta["coverUrl"].(string)); status != http.StatusOK || body != "jpeg" {
		t.Errorf("匿名读取封面 = %d %q", status, body)
	}
	if inputs := transcoder.Inputs(); len(inputs) != 1 || string(inputs[0]) != "平台给的原始音频" {
		t.Errorf("转码的输入 = %q，期望下载到的原始音频", inputs)
	}
	admin := "Authorization: Bearer admin-secret"
	if line, status, _, body := send(t, svc.server, "GET /api/admin/media\n"+admin); status != http.StatusOK || !strings.Contains(string(body), `"key":"`+key+`"`) ||
		!strings.Contains(string(body), `"source":"bilibili"`) || !strings.Contains(string(body), `"link":"https://www.bilibili.com/video/BV1xx411c7mD?p=2"`) {
		t.Errorf("%s = %d %s，期望列出这首 B 站的歌", line, status, body)
	}

	client.write(t, `{"kind":"request","type":"queue.add","id":"a2","data":{"track":{"source":"link","id":"https://www.bilibili.com/video/BV1GJ411x7h7"}}}`)
	failed := client.readUntil(t, func(m map[string]any) bool {
		return m["type"] == "room.activity" && m["data"].(map[string]any)["kind"] == "failed"
	})
	if msg := failed["data"].(map[string]any)["message"]; msg != "《付费视频》没能准备好：这个视频需要付费" {
		t.Errorf("音源拒绝下载时的动态 = %v，期望带着音源给出的原因", msg)
	}
	for result, want := range map[string]float64{"ok": 1, "rejected": 1} {
		if got := metricValue(t, w.worker.metrics, "alisten_prepare_jobs_total", "source", "bilibili", "result", result); got != want {
			t.Errorf("alisten_prepare_jobs_total{bilibili,%s} = %v，期望 %v", result, got, want)
		}
	}
}

func TestLinkCaseVariantsAreOneTrack(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)
	downloads := &links{}
	w := startWorker(t, downloads)
	w.waitReady(t)

	hall := createPersistentRoom(t, svc, "大厅")
	_, ticket := enterRoom(t, svc, hall)
	client, _ := connect(t, svc, hall, ticket)
	const link = "https://example.com/qingtian.mp3"
	add := func(id, variant string) {
		t.Helper()
		client.write(t, `{"kind":"request","type":"queue.add","id":"`+id+`","data":{"track":{"source":"link","id":"`+variant+`"}}}`)
		if reply := client.readUntil(t, func(m map[string]any) bool { return m["kind"] == "response" && m["id"] == id }); reply["error"] != nil {
			t.Fatalf("点 %s 的响应 = %v", variant, reply)
		}
	}
	add("a1", "HTTPS://Example.COM/qingtian.mp3")
	now := client.readUntil(t, playing)["data"].(map[string]any)["nowPlaying"].(map[string]any)
	if id := now["entry"].(map[string]any)["track"].(map[string]any)["id"]; id != link || now["mediaUrl"] != "https://alisten.example/storage/alisten/link/"+media.Key(link)+"/audio.mp3" {
		t.Fatalf("开始播放的曲目 = %v，期望曲目 ID 是 %s", now, link)
	}
	add("a2", "hTTps://EXAMPLE.com/qingtian.mp3")
	queued := client.readUntil(t, func(m map[string]any) bool {
		if m["type"] != "queue.updated" {
			return false
		}
		items := m["data"].(map[string]any)["items"].([]any)
		return len(items) == 1 && items[0].(map[string]any)["status"] == "ready"
	})
	item := queued["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if id := item["track"].(map[string]any)["id"]; id != link {
		t.Errorf("等待队列中的曲目 ID = %v，期望 %s", id, link)
	}
	if n := downloads.downloads(); n != 1 {
		t.Errorf("下载了 %d 次，期望两种写法共用一次下载", n)
	}
	if line, status, _, body := send(t, svc.server, "GET /api/admin/media\nAuthorization: Bearer admin-secret"); status != http.StatusOK || strings.Count(string(body), `"key"`) != 1 || !strings.Contains(string(body), `"key":"`+media.Key(link)+`"`) {
		t.Errorf("%s = %d %s，期望只存了一份", line, status, body)
	}
}

type toneLinks struct{}

func (toneLinks) Download(context.Context, string) (port.LinkDownload, func(), error) {
	return port.LinkDownload{Meta: track.Meta{Title: "正弦波"}, Audio: porttest.LinkFile(porttest.ToneWAV(3*time.Second, 0.5))}, func() {}, nil
}

func TestPreparedGainIsStored(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)
	w := startWorker(t, toneLinks{})
	w.waitReady(t)
	analyzed, err := loudness.AnalyzeReader(bytes.NewReader(porttest.ToneWAV(3*time.Second, 0.5)))
	if err != nil {
		t.Fatal(err)
	}
	gain := analyzed.Coefficient()
	if gain > 0.5 {
		t.Fatalf("正弦波的增益 = %v，期望明显小于 1", gain)
	}

	hall := createPersistentRoom(t, svc, "大厅")
	_, ticket := enterRoom(t, svc, hall)
	client, _ := connect(t, svc, hall, ticket)
	const link = "https://example.com/tone.wav"
	client.write(t, `{"kind":"request","type":"queue.add","id":"a1","data":{"track":{"source":"link","id":"`+link+`"}}}`)
	now := client.readUntil(t, playing)["data"].(map[string]any)["nowPlaying"].(map[string]any)
	if now["gain"] != gain {
		t.Errorf("开始播放时的增益 = %v，期望 %v", now["gain"], gain)
	}
	cfg, err := loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var stored float64
	if err := pool.QueryRow(t.Context(), "SELECT gain FROM tracks WHERE source = 'link' AND track_id = $1", link).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != gain {
		t.Errorf("曲目表中的增益 = %v，期望 %v", stored, gain)
	}
}

func submitLink(t *testing.T, link string) {
	t.Helper()
	cfg, err := loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	inserter, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.New(inserter).Submit(t.Context(), track.Track{Source: track.Link, ID: link}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerWaitsForMigrations(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	downloads := &links{}
	w := startWorker(t, downloads)
	time.Sleep(time.Second)
	if status := w.readyz(t); status != http.StatusServiceUnavailable {
		t.Fatalf("迁移之前 /readyz = %d，期望 503", status)
	}
	if err := runMigrate(newLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	w.waitReady(t)

	submitLink(t, "https://example.com/a.mp3")
	for deadline := time.Now().Add(messageWait); downloads.downloads() == 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("迁移完成之后 worker 没有执行任务：\n%s", w.logs.String())
		}
	}
}

type blockedLinks struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockedLinks) Download(context.Context, string) (port.LinkDownload, func(), error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return port.LinkDownload{}, nil, errors.New("测试结束")
}

// 运行中的任务停不下来时，运维端口在停止 River client 期间仍然可以访问，探针据此摘除 worker。
func TestWorkerIsNotReadyWhileStopping(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	if err := runMigrate(newLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	blocked := &blockedLinks{entered: make(chan struct{}), release: make(chan struct{})}
	w := startWorker(t, blocked)
	// Cleanup 按注册的逆序执行：先放行任务，startWorker 注册的清理才能等到 worker 退出。
	release := sync.OnceFunc(func() { close(blocked.release) })
	t.Cleanup(release)
	w.waitReady(t)
	submitLink(t, "https://example.com/a.mp3")
	select {
	case <-blocked.entered:
	case <-time.After(messageWait):
		t.Fatalf("worker 没有开始执行任务：\n%s", w.logs.String())
	}

	w.stop()
	for deadline := time.Now().Add(messageWait); w.readyz(t) != http.StatusServiceUnavailable; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("退出开始之后 /readyz 仍然返回 200")
		}
	}
	if got := metricValue(t, w.worker.metrics, "alisten_ready"); got != 0 {
		t.Errorf("alisten_ready = %v，期望退出开始之后为 0", got)
	}
	release()
	if err := w.result(); err != nil {
		t.Errorf("worker 退出 = %v", err)
	}
}

func TestWorkerExitsWhenJobsStop(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	if err := runMigrate(newLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	w := startWorker(t, &links{})
	w.waitReady(t)
	if err := w.worker.jobs.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- w.result() }()
	select {
	case err := <-exited:
		if err == nil || !strings.Contains(err.Error(), "River client 意外停止") {
			t.Errorf("worker 退出 = %v，期望 River client 意外停止", err)
		}
	case <-time.After(messageWait):
		t.Fatal("River client 停止之后 worker 没有退出")
	}
}

func TestWorkerExitsAfterFlushingTraces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("worker 子命令拒绝以 root 运行")
	}
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	// worker 启动时会清理 TMPDIR 下自己的目录，不能影响本机上正在运行的 worker。
	t.Setenv("TMPDIR", t.TempDir())
	opsListen := freeAddr(t)
	t.Setenv("ALISTEN_OPS_LISTEN", opsListen)
	unresponsiveCollector(t)
	if err := runMigrate(newLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}

	var logs lockedBuffer
	var runErr error
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runErr = runWorker(newLogger(&logs, slog.LevelInfo))
	}()
	eventually(t, "/readyz 返回 200", finished, func() bool { return readyStatus(opsListen) == http.StatusOK })
	// 执行任务的 span 是 worker 记下的唯一一个 span。任务执行一次就失败，span 随之结束：回环地址会被 yt-dlp 的代理拒绝；
	// 本机没有 yt-dlp 的话，任务在启动 yt-dlp 这一步就失败。
	cfg, err := loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	submitLink(t, "http://127.0.0.1:1/qingtian.mp3")
	eventually(t, "任务执行过一次", finished, func() bool {
		var n int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM river_job WHERE queue = 'ytdlp' AND state IN ('retryable', 'discarded')").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	})
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Minute):
		t.Fatal("收到 SIGTERM 一分钟之后 runWorker 仍没有返回")
	}
	if runErr != nil {
		t.Errorf("收到 SIGTERM 之后 runWorker 返回 %v，期望 nil", runErr)
	}
	want := []string{"已停止执行准备任务", "导出剩下的追踪失败", "worker 已退出"}
	var got []string
	for _, msg := range logs.messages(t) {
		if slices.Contains(want, msg) {
			got = append(got, msg)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("退出的日志依次为 %q，期望 %q", got, want)
	}
}

func TestWorkerSweepsOwnLeftoverTempDirsAtStart(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(tmp, "alisten-link-"+host, "job-1")
	others := filepath.Join(tmp, "alisten-link-another-container", "job-2")
	for _, dir := range []string{own, others} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "audio.mp3"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := prepareWorkDir(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(own); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worker 自己上一次运行留下的目录 %s 还在：%v", own, err)
	}
	if _, err := os.Stat(others); err != nil {
		t.Errorf("其他容器的目录 %s 被动了：%v", others, err)
	}
	if info, err := os.Stat(filepath.Dir(own)); err != nil || !info.IsDir() {
		t.Errorf("worker 自己的目录 %s 应当存在，任务在这里建临时目录：%v", filepath.Dir(own), err)
	}
}
