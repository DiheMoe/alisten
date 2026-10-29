package prepare

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var link = track.Track{Source: track.Link, ID: "https://www.youtube.com/watch?v=x"}

type downloader struct {
	meta     track.Meta
	audio    []byte
	cover    []byte
	coverExt string
	err      error
	links    []string
	released int
	finished func()
}

func (d *downloader) Download(_ context.Context, link string) (port.LinkDownload, func(), error) {
	d.links = append(d.links, link)
	if d.err != nil {
		return port.LinkDownload{}, nil, d.err
	}
	released := &atomic.Bool{}
	result := port.LinkDownload{Meta: d.meta, Audio: downloadedFile{released, d.audio}}
	if d.cover != nil {
		result.Cover, result.CoverExt = downloadedFile{released, d.cover}, d.coverExt
	}
	if d.finished != nil {
		d.finished()
	}
	return result, func() {
		released.Store(true)
		d.released++
	}, nil
}

// 和下载适配器一样，release 之后文件就读不到了。
type downloadedFile struct {
	released *atomic.Bool
	data     []byte
}

func (f downloadedFile) Open() (io.ReadSeekCloser, error) {
	if f.released.Load() {
		return nil, errors.New("文件已经删除")
	}
	return porttest.LinkFile(f.data).Open()
}

// 只准备链接的测试用不到 B 站的音源和转码。
func newLinkRunner(d port.LinkDownloader, store port.MediaStore, tracks port.Tracks, logger *slog.Logger) *MediaRunner {
	return NewMediaRunner(d, nil, nil, store, tracks, logger)
}

func runLink(t *testing.T, d *downloader, store *porttest.MediaStore, tr track.Track) (*tracksTable, error) {
	t.Helper()
	tracks := newTracks()
	err := newLinkRunner(d, store, tracks, discard()).Run(t.Context(), tr)
	if d.err == nil && d.released != len(d.links) {
		t.Errorf("下载成功 %d 次，release 调用了 %d 次，期望每次下载成功之后正好调用一次", len(d.links), d.released)
	}
	return tracks, err
}

func TestLinkRunnerStoresMediaAndResult(t *testing.T) {
	audio := porttest.SilentWAV(2 * time.Second)
	d := &downloader{meta: track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", Duration: 3 * time.Second}, audio: audio, cover: []byte("webp"), coverExt: "webp"}
	store := porttest.NewMediaStore()
	tracks, err := runLink(t, d, store, link)
	if err != nil {
		t.Fatal(err)
	}
	key := media.Key(link.ID)
	if got, ok := store.Object(media.AudioObject(key)); !ok || string(got.Data) != string(audio) || got.ContentType != "audio/mpeg" {
		t.Errorf("音频 = %v, %v", got.ContentType, ok)
	}
	if got, ok := store.Object(media.CoverObject(key, "webp")); !ok || string(got.Data) != "webp" || got.ContentType != "image/webp" {
		t.Errorf("封面 = %+v, %v", got, ok)
	}
	want := port.PreparedTrack{
		Meta:      track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverKey: track.ObjectKey(media.CoverObject(key, "webp")), PageURL: link.ID, Duration: 3 * time.Second},
		Result:    track.Prepared{Duration: 2 * time.Second, Gain: 1},
		MediaKey:  key,
		MediaSize: int64(len(audio)),
	}
	if got, ok := tracks.row(link); !ok || got != want {
		t.Errorf("曲目表中的结果 = %+v\n期望 %+v：时长取解码得到的时长", got, want)
	}
	if !slices.Equal(d.links, []string{link.ID}) {
		t.Errorf("下载的链接 = %q", d.links)
	}
}

func TestLinkRunnerSkipsCoverOfOtherFormats(t *testing.T) {
	d := &downloader{meta: track.Meta{Title: "晴天"}, audio: porttest.SilentWAV(time.Second), cover: []byte("<svg/>"), coverExt: "svg"}
	store := porttest.NewMediaStore()
	tracks, err := runLink(t, d, store, link)
	if err != nil {
		t.Fatal(err)
	}
	if keys := store.Keys(); !slices.Equal(keys, []string{media.AudioObject(media.Key(link.ID))}) {
		t.Errorf("上传的对象 = %q，期望只有音频", keys)
	}
	if got, _ := tracks.row(link); got.Meta.CoverKey != "" {
		t.Errorf("封面 = %q，期望没有封面", got.Meta.CoverKey)
	}
}

func TestLinkRunnerFallsBackWhenAudioCannotBeAnalyzed(t *testing.T) {
	d := &downloader{meta: track.Meta{Title: "晴天", Duration: 3 * time.Second}, audio: []byte("不是音频")}
	tracks, err := runLink(t, d, porttest.NewMediaStore(), link)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := tracks.row(link); got.Result != (track.Prepared{Duration: 3 * time.Second, Gain: 1}) {
		t.Errorf("无法解码时的结果 = %+v，期望增益为 1、时长取元数据中的时长", got.Result)
	}

	d = &downloader{meta: track.Meta{Title: "晴天"}, audio: []byte("不是音频")}
	var rejected *port.RejectedError
	if tracks, err := runLink(t, d, porttest.NewMediaStore(), link); !errors.As(err, &rejected) {
		t.Errorf("元数据中也没有时长时的错误 = %v，期望不能准备", err)
	} else if _, ok := tracks.row(link); ok {
		t.Error("不能准备的歌不应写入曲目表")
	}
}

func TestLinkRunnerLogsAudioThatCannotBeDecoded(t *testing.T) {
	decodeErr := truncatedMP3Error(t)
	runWith := func(m track.Meta) (*bytes.Buffer, error) {
		logs := &bytes.Buffer{}
		d := &downloader{meta: m, audio: truncatedMP3}
		err := newLinkRunner(d, porttest.NewMediaStore(), newTracks(), slog.New(slog.NewJSONHandler(logs, nil))).Run(t.Context(), link)
		return logs, err
	}

	t.Run("回退", func(t *testing.T) {
		logs, err := runWith(track.Meta{Title: "晴天", Duration: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		checkDecodeWarning(t, logs, link, decodeErr)
	})
	t.Run("元数据中也没有时长", func(t *testing.T) {
		_, err := runWith(track.Meta{Title: "晴天"})
		var rejected *port.RejectedError
		if !errors.As(err, &rejected) || err.Error() != "这首歌的音频无法播放" {
			t.Errorf("错误 = %v，期望不能准备，原因带着解码的错误 %q", err, decodeErr)
		}
	})
}

func TestLinkRunnerFailures(t *testing.T) {
	var rejected *port.RejectedError
	refused := &port.RejectedError{Err: errors.New("yt-dlp 不支持这个链接")}
	d := &downloader{err: refused}
	store := porttest.NewMediaStore()
	if _, err := runLink(t, d, store, link); !errors.As(err, &rejected) || err.Error() != "yt-dlp 不支持这个链接" {
		t.Errorf("下载被拒绝时的错误 = %v", err)
	}

	for _, tr := range []track.Track{{Source: track.Link, ID: "ftp://example.com/a.mp3"}, {Source: track.Link, ID: "https://:8080/a.mp3"}, {Source: track.NetEase, ID: "186016"}} {
		d := &downloader{audio: porttest.SilentWAV(time.Second)}
		if _, err := runLink(t, d, store, tr); !errors.As(err, &rejected) || len(d.links) != 0 {
			t.Errorf("任务参数 %v 的错误 = %v，下载了 %q，期望不能准备", tr, err, d.links)
		}
	}

	store.Fail(errors.New("连接被拒绝"))
	d = &downloader{meta: track.Meta{Title: "晴天"}, audio: porttest.SilentWAV(time.Second)}
	tracks, err := runLink(t, d, store, link)
	if err == nil || errors.As(err, &rejected) {
		t.Errorf("上传失败时的错误 = %v，期望可以重试的错误", err)
	}
	if _, ok := tracks.row(link); ok {
		t.Error("上传失败时不应写入曲目表")
	}
}

func TestLinkRunnerAnalysisStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d := &downloader{meta: track.Meta{Title: "晴天", Duration: time.Second}, audio: porttest.SilentWAV(2 * time.Second), finished: cancel}
	tracks := newTracks()
	err := newLinkRunner(d, porttest.NewMediaStore(), tracks, discard()).Run(ctx, link)
	var rejected *port.RejectedError
	if !errors.Is(err, context.Canceled) || errors.As(err, &rejected) {
		t.Errorf("分析时 ctx 已经结束，Run = %v，期望可以重试的 context.Canceled", err)
	}
	if _, ok := tracks.row(link); ok {
		t.Error("没有分析完的歌不应写入曲目表")
	}
}

type checkedStore struct {
	*porttest.MediaStore
	putErr    func(key string) error
	deleteErr error

	mu        sync.Mutex
	uploaded  bool
	deadlines []bool
}

func (s *checkedStore) Put(ctx context.Context, key string, body io.ReadSeeker, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.putErr != nil {
		if err := s.putErr(key); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.uploaded = true
	s.mu.Unlock()
	return s.MediaStore.Put(ctx, key, body, contentType)
}

func (s *checkedStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, hasDeadline := ctx.Deadline()
	s.mu.Lock()
	s.deadlines = append(s.deadlines, hasDeadline)
	failing := s.uploaded && s.deleteErr != nil
	s.mu.Unlock()
	if failing {
		return s.deleteErr
	}
	return s.MediaStore.Delete(ctx, key)
}

type failingSave struct{ *tracksTable }

func (failingSave) SavePreparedTrack(context.Context, track.Track, port.PreparedTrack) error {
	return errors.New("数据库不可用")
}

func storeWithOtherObject(t *testing.T) *checkedStore {
	t.Helper()
	store := &checkedStore{MediaStore: porttest.NewMediaStore()}
	if err := store.MediaStore.Put(t.Context(), media.AudioObject("other"), strings.NewReader("x"), "audio/mpeg"); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestLinkRunnerClearsStaleObjects(t *testing.T) {
	key := media.Key(link.ID)
	for name, tt := range map[string]struct {
		cover    []byte
		coverExt string
		want     []string
	}{
		"cover_format_changed": {[]byte("jpeg"), "jpg", []string{media.AudioObject("other"), media.AudioObject(key), media.CoverObject(key, "jpg")}},
		"no_cover_this_time":   {nil, "", []string{media.AudioObject("other"), media.AudioObject(key)}},
	} {
		t.Run(name, func(t *testing.T) {
			store := storeWithOtherObject(t)
			for _, object := range []string{media.AudioObject(key), media.CoverObject(key, "png"), media.CoverObject(key, "webp"), media.CoverObject(key, "jpeg")} {
				if err := store.MediaStore.Put(t.Context(), object, strings.NewReader("old"), "application/octet-stream"); err != nil {
					t.Fatal(err)
				}
			}
			d := &downloader{meta: track.Meta{Title: "晴天"}, audio: porttest.SilentWAV(time.Second), cover: tt.cover, coverExt: tt.coverExt}
			if err := newLinkRunner(d, store, newTracks(), discard()).Run(t.Context(), link); err != nil {
				t.Fatal(err)
			}
			if got := store.Keys(); !slices.Equal(got, slices.Sorted(slices.Values(tt.want))) {
				t.Errorf("对象 = %q，期望 %q", got, tt.want)
			}
			if got, _ := store.Object(media.AudioObject(key)); string(got.Data) == "old" {
				t.Error("音频还是上一次的")
			}
		})
	}
}

func TestLinkRunnerRemovesUploadedObjectsOnFailure(t *testing.T) {
	key := media.Key(link.ID)
	cover := media.CoverObject(key, "jpg")
	tests := map[string]struct {
		d      *downloader
		store  func(cancel context.CancelFunc) *checkedStore
		tracks func() port.Tracks
		check  func(t *testing.T, err error)
	}{
		"audio_cannot_be_analyzed": {
			d: &downloader{audio: []byte("不是音频"), cover: []byte("jpeg"), coverExt: "jpg"},
			check: func(t *testing.T, err error) {
				var rejected *port.RejectedError
				if !errors.As(err, &rejected) {
					t.Errorf("错误 = %v，期望不能准备", err)
				}
			},
		},
		"result_cannot_be_saved": {
			d:      &downloader{meta: track.Meta{Title: "晴天"}, audio: porttest.SilentWAV(time.Second), cover: []byte("jpeg"), coverExt: "jpg"},
			tracks: func() port.Tracks { return failingSave{newTracks()} },
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "数据库不可用") {
					t.Errorf("错误 = %v，期望写入曲目表的错误", err)
				}
			},
		},
		"cover_cannot_be_uploaded": {
			d: &downloader{meta: track.Meta{Title: "晴天"}, audio: porttest.SilentWAV(time.Second), cover: []byte("jpeg"), coverExt: "jpg"},
			store: func(context.CancelFunc) *checkedStore {
				s := storeWithOtherObject(t)
				s.putErr = func(key string) error {
					if key == cover {
						return errors.New("磁盘已满")
					}
					return nil
				}
				return s
			},
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "磁盘已满") {
					t.Errorf("错误 = %v，期望封面上传的错误", err)
				}
			},
		},
		"cancelled_while_uploading": {
			d: &downloader{meta: track.Meta{Title: "晴天"}, audio: porttest.SilentWAV(time.Second), cover: []byte("jpeg"), coverExt: "jpg"},
			store: func(cancel context.CancelFunc) *checkedStore {
				s := storeWithOtherObject(t)
				s.putErr = func(key string) error {
					if key == cover {
						cancel()
						return context.Canceled
					}
					return nil
				}
				return s
			},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("错误 = %v，期望 context.Canceled", err)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := storeWithOtherObject(t)
			if tt.store != nil {
				store = tt.store(cancel)
			}
			var tracks port.Tracks = newTracks()
			if tt.tracks != nil {
				tracks = tt.tracks()
			}
			err := newLinkRunner(tt.d, store, tracks, discard()).Run(ctx, link)
			tt.check(t, err)
			if got := store.Keys(); !slices.Equal(got, []string{media.AudioObject("other")}) {
				t.Errorf("失败之后的对象 = %q，期望已上传的音频和封面都删掉", got)
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			// 上传之前的清理会删除音频和每种格式的封面各 1 个对象，之后的每次清理都应带有期限。
			if before := len(coverTypes) + 1; len(store.deadlines) <= before || slices.Contains(store.deadlines[before:], false) {
				t.Errorf("每次删除时 ctx 是否带期限 = %v，期望上传之后有清理，并且都带着期限", store.deadlines)
			}
		})
	}
}

func TestLinkRunnerCleanupFailureIsLogged(t *testing.T) {
	var logs bytes.Buffer
	store := &checkedStore{MediaStore: porttest.NewMediaStore(), deleteErr: errors.New("连接被拒绝")}
	d := &downloader{audio: []byte("不是音频")}
	err := newLinkRunner(d, store, newTracks(), slog.New(slog.NewJSONHandler(&logs, nil))).Run(t.Context(), link)
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) {
		t.Errorf("错误 = %v，期望仍是不能准备", err)
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), "连接被拒绝") || !strings.Contains(logs.String(), link.ID) {
		t.Errorf("日志 = %s，期望一条带着错误和链接的 WARN", logs.String())
	}
}

func TestLinkRunnerUnanalyzableAudioWinsOverUploadFailure(t *testing.T) {
	store := &checkedStore{MediaStore: porttest.NewMediaStore(), putErr: func(string) error { return errors.New("连接被拒绝") }}
	d := &downloader{audio: []byte("不是音频")}
	err := newLinkRunner(d, store, newTracks(), discard()).Run(t.Context(), link)
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) {
		t.Errorf("上传失败且无法分析时 = %v，期望不能准备", err)
	}
}
