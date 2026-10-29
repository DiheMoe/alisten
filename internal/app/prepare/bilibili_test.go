package prepare

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var video = track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD/2"}

type bilibiliFixture struct {
	source     *porttest.Bilibili
	transcoder *porttest.Transcoder
	store      *porttest.MediaStore
	tracks     *tracksTable
}

// 转出的 mp3 总是一段 2 秒的静音。
func newBilibiliFixture() bilibiliFixture {
	return bilibiliFixture{source: porttest.NewBilibili(), transcoder: porttest.NewTranscoder(porttest.SilentWAV(2 * time.Second)), store: porttest.NewMediaStore(), tracks: newTracks()}
}

func (f bilibiliFixture) run(t *testing.T, tr track.Track) error {
	t.Helper()
	return NewMediaRunner(&downloader{}, f.source, f.transcoder, f.store, f.tracks, discard()).Run(t.Context(), tr)
}

func TestMediaRunnerStoresBilibili(t *testing.T) {
	f := newBilibiliFixture()
	original := []byte("平台给的原始音频")
	f.source.AddVideo(video.ID, track.Meta{Title: "晴天（钢琴版）", Artist: "某 UP 主", Duration: 3 * time.Second}, original, []byte("jpeg"), "jpg")
	if err := f.run(t, video); err != nil {
		t.Fatal(err)
	}
	if inputs := f.transcoder.Inputs(); len(inputs) != 1 || string(inputs[0]) != string(original) {
		t.Errorf("转码的输入 = %q，期望平台给的原始音频", inputs)
	}
	key := media.Key(video.ID)
	mp3 := porttest.SilentWAV(2 * time.Second)
	if got, ok := f.store.Object(media.AudioObject(key)); !ok || string(got.Data) != string(mp3) || got.ContentType != "audio/mpeg" {
		t.Errorf("上传的音频 = %v, %v，期望转出的 mp3", got.ContentType, ok)
	}
	if got, ok := f.store.Object(media.CoverObject(key, "jpg")); !ok || string(got.Data) != "jpeg" || got.ContentType != "image/jpeg" {
		t.Errorf("上传的封面 = %+v, %v", got, ok)
	}
	want := port.PreparedTrack{
		Meta:      track.Meta{Title: "晴天（钢琴版）", Artist: "某 UP 主", CoverKey: track.ObjectKey(media.CoverObject(key, "jpg")), PageURL: "https://www.bilibili.com/video/BV1xx411c7mD?p=2", Duration: 3 * time.Second},
		Result:    track.Prepared{Duration: 2 * time.Second, Gain: 1},
		MediaKey:  key,
		MediaSize: int64(len(mp3)),
	}
	if got, ok := f.tracks.row(video); !ok || got != want {
		t.Errorf("曲目表中的结果 = %+v\n期望 %+v：时长取解码转出的 mp3 得到的时长", got, want)
	}
	if f.source.Released() != 1 || f.transcoder.Released() != 1 {
		t.Errorf("释放了 %d 次下载、%d 次转码，期望各 1 次", f.source.Released(), f.transcoder.Released())
	}
}

func TestMediaRunnerRejectsBadBilibiliTasks(t *testing.T) {
	f := newBilibiliFixture()
	f.source.AddVideo("BV1xx411c7mD", track.Meta{Title: "付费视频"}, []byte("平台给的原始音频"), nil, "")
	f.source.RejectVideo("BV1xx411c7mD", "这个视频需要付费")
	var rejected *port.RejectedError
	if err := f.run(t, track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD"}); !errors.As(err, &rejected) || err.Error() != "这个视频需要付费" {
		t.Errorf("音源拒绝下载时 = %v，期望不能准备，原因是音源给出的", err)
	}
	// 音源里也登记这些 ID：交给音源就会下载成功。
	for _, id := range []string{"BV1xx411c7mD/1", "../BV1xx411c7mD", ""} {
		f.source.AddVideo(id, track.Meta{Title: "格式不对的 ID"}, []byte("平台给的原始音频"), nil, "")
		if err := f.run(t, track.Track{Source: track.Bilibili, ID: id}); !errors.As(err, &rejected) {
			t.Errorf("任务参数中的 ID %q 的错误 = %v，期望不能准备", id, err)
		}
	}
	if f.source.Released() != 0 || len(f.transcoder.Inputs()) != 0 || len(f.store.Keys()) != 0 {
		t.Errorf("释放了 %d 次下载、转码了 %d 次、上传了 %q，期望不能准备的歌都不下载、不转码、不上传", f.source.Released(), len(f.transcoder.Inputs()), f.store.Keys())
	}
}

func TestMediaRunnerReleasesDownloadWhenTranscodeFails(t *testing.T) {
	f := newBilibiliFixture()
	f.source.AddVideo(video.ID, track.Meta{Title: "晴天（钢琴版）"}, []byte("平台给的原始音频"), nil, "")
	f.transcoder.Fail(errors.New("ffmpeg 转码失败：Invalid data found when processing input"))
	err := f.run(t, video)
	var rejected *port.RejectedError
	if err == nil || errors.As(err, &rejected) {
		t.Errorf("转码失败时 = %v，期望可以重试的错误", err)
	}
	if f.source.Released() != 1 {
		t.Errorf("转码失败之后释放了 %d 次下载，期望 1 次", f.source.Released())
	}
	if len(f.store.Keys()) != 0 {
		t.Error("转码失败时不应上传")
	}
	if _, ok := f.tracks.row(video); ok {
		t.Error("转码失败时不应写入曲目表")
	}
}

type panickingTranscoder struct{}

func (panickingTranscoder) ToMP3(context.Context, port.LinkFile) (port.LinkFile, func(), error) {
	panic("转码出了程序缺陷")
}

func TestMediaRunnerReleasesDownloadWhenTranscoderPanics(t *testing.T) {
	source := porttest.NewBilibili()
	source.AddVideo(video.ID, track.Meta{Title: "晴天（钢琴版）"}, []byte("平台给的原始音频"), nil, "")
	runner := NewMediaRunner(&downloader{}, source, panickingTranscoder{}, porttest.NewMediaStore(), newTracks(), discard())
	func() {
		defer func() { _ = recover() }()
		_ = runner.Run(t.Context(), video)
	}()
	if source.Released() != 1 {
		t.Errorf("转码 panic 之后释放了 %d 次下载，期望 1 次：River 恢复 panic 之后 worker 继续运行，文件不能留下", source.Released())
	}
}
