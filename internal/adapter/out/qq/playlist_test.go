package qq

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestPlaylistTracks(t *testing.T) {
	c, obs := serve(t, map[string]http.HandlerFunc{
		"/qzone-music/fcg-bin/fcg_ucc_getcdinfo_byids_cp.fcg": func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("disstid"); got != "7382629476" {
				t.Errorf("歌单 ID = %q，期望与搜索返回的 dissid 一致", got)
			}
			fixture(t, "playlist-tracks.json")(w, r)
		},
	})
	want := []port.Song{
		{
			Track: track.Track{Source: track.QQ, ID: "mid1"},
			Meta: track.Meta{
				Title: "第一首", Artist: "歌手甲, 歌手乙", Album: "专辑一",
				CoverURL: "https://y.gtimg.cn/music/photo_new/T002R300x300M000album1.jpg",
				PageURL:  "https://y.qq.com/n/ryqq/songDetail/mid1",
				Duration: 200 * time.Second,
			},
		},
		{
			Track: track.Track{Source: track.QQ, ID: "mid2"},
			Meta: track.Meta{
				Title: "第二首", Artist: "歌手丙", Album: "专辑二",
				CoverURL: "https://y.gtimg.cn/music/photo_new/T002R300x300M000album2.jpg",
				PageURL:  "https://y.qq.com/n/ryqq/songDetail/mid2",
				Duration: 100 * time.Second,
			},
		},
	}
	// 缓存命中时第二次调用不再发请求。
	for range 2 {
		got, err := c.PlaylistTracks(t.Context(), "7382629476")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("歌单内的歌曲 = %#v\n期望 %#v", got, want)
		}
	}
	obs.check(t, "qq playlist_tracks")
}

func TestPlaylistTracksUnavailable(t *testing.T) {
	for name, file := range map[string]string{
		"接口返回错误码":   "playlist-tracks-missing.json",
		"cdlist 为空": "playlist-tracks-empty.json",
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := serve(t, map[string]http.HandlerFunc{
				"/qzone-music/fcg-bin/fcg_ucc_getcdinfo_byids_cp.fcg": fixture(t, file),
			})
			if _, err := c.PlaylistTracks(t.Context(), "999999999999"); err == nil {
				t.Error("不存在或不可见的歌单没有返回错误")
			}
		})
	}
}
