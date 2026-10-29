package bilibili

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type viewData struct {
	BVID  string `json:"bvid"`
	Title string `json:"title"`
	Pic   string `json:"pic"`
	Owner struct {
		Name string `json:"name"`
		Mid  int64  `json:"mid"`
	} `json:"owner"`
}

type pageInfo struct {
	CID      int64  `json:"cid"`
	Page     int    `json:"page"`
	Part     string `json:"part"`
	Duration int    `json:"duration"`
}

type videoInfo struct {
	cid int64
	pic string
}

func (c *Client) TrackMeta(ctx context.Context, id string) (track.Meta, error) {
	bvid, page, ok := track.ParseBilibiliID(id)
	if !ok {
		return track.Meta{}, fmt.Errorf("不是 Bilibili 曲目 ID：%q", id)
	}
	meta, _, err := c.trackMeta(ctx, "meta_view", "meta_pagelist", bvid, page)
	return meta, err
}

// trackMeta 取标题、UP 主和时长；CoverURL 和 PageURL 留空：封面在准备时下载存储，页面地址由调用方按 ID 拼出。
// pagelist 是存在性判断和 cid 的来源；view 接口经常被风控（未登录 412、登录后伪装 -404），
// 失败时退化为 pagelist 的元数据：标题用分 P 标题（分 P 标题为空时用 BV 号），UP 主和封面缺失。
func (c *Client) trackMeta(ctx context.Context, viewOp, pagelistOp, bvid string, page int) (track.Meta, videoInfo, error) {
	cookie, err := c.cookie(ctx)
	if err != nil {
		return track.Meta{}, videoInfo{}, err
	}
	pages, err := c.fetchPagelist(ctx, pagelistOp, bvid, cookie)
	if err != nil {
		return track.Meta{}, videoInfo{}, err
	}
	p, ok := findPage(pages, page)
	if !ok {
		return track.Meta{}, videoInfo{}, &port.RejectedError{Err: fmt.Errorf("这个视频没有第 %d P", page)}
	}
	meta := track.Meta{Title: p.Part, Duration: time.Duration(p.Duration) * time.Second}
	if meta.Title == "" {
		meta.Title = bvid
	}
	info := videoInfo{cid: p.CID}
	view, err := c.fetchView(ctx, viewOp, bvid, cookie)
	if err != nil {
		return meta, info, nil
	}
	title := view.Title
	if p.Part != "" && p.Part != view.Title {
		title = view.Title + " - " + p.Part
	}
	meta.Title, meta.Artist = title, view.Owner.Name
	info.pic = view.Pic
	return meta, info, nil
}

func (c *Client) fetchView(ctx context.Context, op, bvid, cookie string) (d viewData, err error) {
	start := time.Now()
	defer func() { c.report(op, start, err) }()
	err = c.getData(ctx, viewURL+"?bvid="+url.QueryEscape(bvid), cookie, &d)
	return d, err
}

func (c *Client) fetchPagelist(ctx context.Context, op, bvid, cookie string) (pages []pageInfo, err error) {
	start := time.Now()
	defer func() { c.report(op, start, err) }()
	err = c.getData(ctx, pagelistURL+"?bvid="+url.QueryEscape(bvid), cookie, &pages)
	return pages, err
}

func findPage(pages []pageInfo, page int) (pageInfo, bool) {
	for _, p := range pages {
		if p.Page == page {
			return p, true
		}
	}
	return pageInfo{}, false
}
