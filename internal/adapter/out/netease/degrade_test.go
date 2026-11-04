package netease

import (
	"context"
	"net/http"
	"testing"
)

// 歌词接口失败：就地重试一次仍失败则降级为空歌词，元数据和音频不受影响。
func TestResolveDegradesOnLyricInterfaceFailure(t *testing.T) {
	calls := 0
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url.json")
	routes["/weapi/song/lyric/v1"] = func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}
	c, obs := serve(t, routes)
	got, err := c.Resolve(t.Context(), "3387653989")
	if err != nil {
		t.Fatal(err)
	}
	if got.Lyric != "" || got.LyricWords != "" || got.LyricTrans != "" {
		t.Errorf("降级后的歌词 = %q %q %q，期望全空", got.Lyric, got.LyricWords, got.LyricTrans)
	}
	if got.Meta != cover.Meta {
		t.Errorf("元数据 = %#v", got.Meta)
	}
	if audio := readAudio(t, got); audio != "ID3 音频" {
		t.Errorf("音频 = %q", audio)
	}
	if calls != 2 {
		t.Errorf("歌词接口调用了 %d 次，期望失败后重试一次", calls)
	}
	obs.check(t, "track_detail", "lyric 失败", "lyric 失败", "play_url")
}

// 第一次失败、重试成功时歌词正常返回。
func TestResolveRetriesLyricOnce(t *testing.T) {
	calls := 0
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url.json")
	routes["/weapi/song/lyric/v1"] = func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fixture(t, "lyric.json")(w, r)
	}
	c, obs := serve(t, routes)
	got, err := c.Resolve(t.Context(), "3387653989")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Audio.Close() }()
	if got.Lyric == "" || got.LyricWords == "" || got.LyricTrans == "" {
		t.Errorf("重试成功后的歌词 = %q %q %q", got.Lyric, got.LyricWords, got.LyricTrans)
	}
	if calls != 2 {
		t.Errorf("歌词接口调用了 %d 次，期望一次失败一次成功", calls)
	}
	obs.check(t, "track_detail", "lyric 失败", "lyric", "play_url")
}

// 调用方 ctx 被取消时返回错误，不降级：任务退出时中断，不能把空歌词写进曲目表。
func TestResolveDoesNotDegradeWhenCtxCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url.json")
	routes["/weapi/song/lyric/v1"] = func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusBadGateway)
	}
	c, _ := serve(t, routes)
	if _, err := c.Resolve(ctx, "3387653989"); err == nil {
		t.Fatal("ctx 取消时 Resolve 没有返回错误")
	}
}
