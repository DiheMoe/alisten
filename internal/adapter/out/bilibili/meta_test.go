package bilibili

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const viewResponse = `{"code":0,"message":"0","data":{"bvid":"BV1xx411c7mD","title":"视频标题","pic":"http://i0.hdslb.com/bfs/archive/cover.jpg","owner":{"name":"UP主名字","mid":99},"pages":[{"cid":111,"page":1,"part":"","duration":200}]}}`

func metaRoutes(pagelist string) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/x/web-interface/view": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("bvid") != "BV1xx411c7mD" {
				http.Error(w, "bad bvid", http.StatusBadRequest)
				return
			}
			respondJSON(w, viewResponse)
		},
		"/x/player/pagelist": func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, pagelist)
		},
	}
}

const singlePage = `{"code":0,"message":"0","data":[{"cid":111,"page":1,"part":"","duration":200}]}`

const twoPages = `{"code":0,"message":"0","data":[{"cid":111,"page":1,"part":"","duration":200},{"cid":222,"page":2,"part":"第二P","duration":120}]}`

func TestTrackMetaSinglePage(t *testing.T) {
	c, tr, obs, creds := serve(t, metaRoutes(singlePage))
	creds.set(t, "SESSDATA=sess123", "")
	meta, err := c.TrackMeta(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	want := track.Meta{Title: "视频标题", Artist: "UP主名字", Duration: 200 * time.Second}
	if meta != want {
		t.Fatalf("元数据 = %+v，期望 %+v", meta, want)
	}
	for _, r := range tr.requests() {
		if r.cookie != "SESSDATA=sess123" || r.referer != "https://www.bilibili.com/" {
			t.Errorf("请求头不对：%+v", r)
		}
	}
	obs.check(t, "bilibili:meta_pagelist:ok", "bilibili:meta_view:ok")
}

func TestTrackMetaSecondPage(t *testing.T) {
	c, _, _, _ := serve(t, metaRoutes(twoPages))
	meta, err := c.TrackMeta(t.Context(), "BV1xx411c7mD/2")
	if err != nil {
		t.Fatal(err)
	}
	want := track.Meta{Title: "视频标题 - 第二P", Artist: "UP主名字", Duration: 120 * time.Second}
	if meta != want {
		t.Fatalf("元数据 = %+v，期望 %+v", meta, want)
	}
}

func TestTrackMetaPageMissing(t *testing.T) {
	c, _, _, _ := serve(t, metaRoutes(twoPages))
	_, err := c.TrackMeta(t.Context(), "BV1xx411c7mD/3")
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("不存在的分 P = %v，期望 RejectedError", err)
	}
}

// pagelist 返回 -400（不存在的视频）时拒绝准备。
func TestTrackMetaRejected(t *testing.T) {
	c, _, obs, _ := serve(t, map[string]http.HandlerFunc{
		"/x/player/pagelist": func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, `{"code":-400,"message":"请求错误","ttl":1}`)
		},
	})
	_, err := c.TrackMeta(t.Context(), "BV1xx411c7mD")
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("视频不存在 = %v，期望 RejectedError", err)
	}
	obs.check(t, "bilibili:meta_pagelist:error")
}

// view 接口经常被风控（未登录 412、登录后伪装 -404），失败时退化为 pagelist 的元数据。
func TestTrackMetaViewFallback(t *testing.T) {
	routes := metaRoutes(twoPages)
	routes["/x/web-interface/view"] = func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, `{"code":-404,"message":"啥都木有","data":null}`)
	}
	c, _, obs, _ := serve(t, routes)
	meta, err := c.TrackMeta(t.Context(), "BV1xx411c7mD/2")
	if err != nil {
		t.Fatal(err)
	}
	want := track.Meta{Title: "第二P", Duration: 120 * time.Second}
	if meta != want {
		t.Fatalf("降级后的元数据 = %+v，期望 %+v", meta, want)
	}
	obs.check(t, "bilibili:meta_pagelist:ok", "bilibili:meta_view:error")
}

// 单 P 视频的分 P 标题为空时，降级用 BV 号做标题。
func TestTrackMetaViewFallbackUntitled(t *testing.T) {
	routes := metaRoutes(singlePage)
	routes["/x/web-interface/view"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}
	c, _, _, _ := serve(t, routes)
	meta, err := c.TrackMeta(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "BV1xx411c7mD" || meta.Artist != "" {
		t.Fatalf("降级后的元数据 = %+v，期望标题为 BV 号、歌手为空", meta)
	}
}

func TestTrackMetaPagelistFailure(t *testing.T) {
	routes := metaRoutes(singlePage)
	routes["/x/player/pagelist"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c, _, obs, _ := serve(t, routes)
	if _, err := c.TrackMeta(t.Context(), "BV1xx411c7mD"); err == nil {
		t.Fatal("pagelist 失败时没有返回错误")
	}
	// pagelist 是第一步，失败时不应再请求 view。
	obs.check(t, "bilibili:meta_pagelist:error")
}

func TestTrackMetaInvalidID(t *testing.T) {
	c, tr, _, _ := serve(t, nil)
	if _, err := c.TrackMeta(t.Context(), "nonsense"); err == nil {
		t.Fatal("非法 ID 没有返回错误")
	}
	if len(tr.requests()) != 0 {
		t.Fatalf("非法 ID 不应请求平台，请求了 %+v", tr.requests())
	}
}
