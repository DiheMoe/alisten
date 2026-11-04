package qq

import (
	"context"
	"io"
	"net/http"
	"testing"
)

var okPlayResponse = `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"F000004Fs2FP1EvZYc004Fs2FP1EvZYc.flac","purl":"` + audioPath[1:] + `"}]}}}`

func readQQAudio(t *testing.T, audio io.ReadCloser) string {
	t.Helper()
	defer func() { _ = audio.Close() }()
	b, err := io.ReadAll(audio)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 歌词接口失败：就地重试一次仍失败则降级为空歌词，元数据和音频不受影响。
func TestResolveDegradesOnLyricInterfaceFailure(t *testing.T) {
	calls := 0
	routes := resolveRoutes(t)
	routes["/cgi-bin/musicu.fcg"] = musicuLyric(t, okPlayResponse, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	})
	c, obs := serve(t, routes)
	got, err := c.Resolve(t.Context(), "004Fs2FP1EvZYc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Lyric != "" || got.LyricWords != "" || got.LyricTrans != "" {
		t.Errorf("降级后的歌词 = %q %q %q，期望全空", got.Lyric, got.LyricWords, got.LyricTrans)
	}
	if got.Meta != live.Meta {
		t.Errorf("元数据 = %#v", got.Meta)
	}
	if audio := readQQAudio(t, got.Audio); audio != "ID3 音频" {
		t.Errorf("音频 = %q", audio)
	}
	if calls != 2 {
		t.Errorf("歌词接口调用了 %d 次，期望失败后重试一次", calls)
	}
	obs.check(t, "qq track_detail", "qq lyric 失败", "qq lyric 失败", "qq play_url")
}

// 第一次失败、重试成功时歌词正常返回。
func TestResolveRetriesLyricOnce(t *testing.T) {
	calls := 0
	routes := resolveRoutes(t)
	routes["/cgi-bin/musicu.fcg"] = musicuLyric(t, okPlayResponse, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fixture(t, "lyric.json")(w, r)
	})
	c, obs := serve(t, routes)
	got, err := c.Resolve(t.Context(), "004Fs2FP1EvZYc")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Audio.Close() }()
	if got.Lyric != "[ti:逐字测试]\n[ar:歌手]\n[00:01.00]你好世\n[00:03.00]再见\n" || got.LyricWords == "" || got.LyricTrans == "" {
		t.Errorf("重试成功后的歌词 = %q %q %q", got.Lyric, got.LyricWords, got.LyricTrans)
	}
	if calls != 2 {
		t.Errorf("歌词接口调用了 %d 次，期望一次失败一次成功", calls)
	}
	obs.check(t, "qq track_detail", "qq lyric 失败", "qq lyric", "qq play_url")
}

// 解密失败重试结果也不会变，直接降级，不重试。
func TestResolveDegradesOnLyricDecryptFailure(t *testing.T) {
	calls := 0
	routes := resolveRoutes(t)
	routes["/cgi-bin/musicu.fcg"] = musicuLyric(t, okPlayResponse, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		body(`{"code":0,"request":{"code":0,"data":{"lyric":"deadbeefzz","trans":""}}}`)(w, nil)
	})
	c, obs := serve(t, routes)
	got, err := c.Resolve(t.Context(), "004Fs2FP1EvZYc")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Audio.Close() }()
	if got.Lyric != "" || got.LyricWords != "" || got.LyricTrans != "" {
		t.Errorf("解密失败降级后的歌词 = %q %q %q，期望全空", got.Lyric, got.LyricWords, got.LyricTrans)
	}
	if calls != 1 {
		t.Errorf("解密失败后歌词接口调用了 %d 次，期望不重试", calls)
	}
	// 解密在接口调用成功之后，观测上歌词请求本身没有失败，降级由 WARN 日志体现。
	obs.check(t, "qq track_detail", "qq lyric", "qq play_url")
}

// 调用方 ctx 被取消时返回错误，不降级：任务退出时中断，不能把空歌词写进曲目表。
func TestResolveDoesNotDegradeWhenCtxCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	routes := resolveRoutes(t)
	routes["/cgi-bin/musicu.fcg"] = musicuLyric(t, okPlayResponse, func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusBadGateway)
	})
	c, _ := serve(t, routes)
	if _, err := c.Resolve(ctx, "004Fs2FP1EvZYc"); err == nil {
		t.Fatal("ctx 取消时 Resolve 没有返回错误")
	}
}
