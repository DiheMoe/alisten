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
		return track.Meta{}, fmt.Errorf("不是 哔哩哔哩曲目 ID：%q", id)
	}
	cookie, err := c.cookie.Get(ctx)
	if err != nil {
		return track.Meta{}, err
	}
	meta, _, err := c.trackMeta(ctx, "meta_view", "meta_pagelist", bvid, page, cookie)
	return meta, err
}

// CoverURL 和 PageURL 都留空，因为封面在准备阶段下载到对象存储，页面地址由调用方按曲目 ID 拼出。
// pagelist 提供分 P 存在性判断和 cid。view 接口常被风控拦截（未登录返回 412，登录后也可能伪装成 -404），
// 所以 view 失败时退回使用 pagelist 的分 P 标题（为空则用 BV 号）。
func (c *Client) trackMeta(ctx context.Context, viewOp, pagelistOp, bvid string, page int, cookie string) (track.Meta, videoInfo, error) {
	pages, err := c.fetchPagelist(ctx, pagelistOp, bvid, cookie)
	if err != nil {
		return track.Meta{}, videoInfo{}, err
	}
	p, ok := findPage(pages, page)
	if !ok {
		return track.Meta{}, videoInfo{}, &port.RejectedError{Err: fmt.Errorf("这个视频没有第 %d P", page)}
	}
	part := cleanText(p.Part)
	duration := min(max(0, time.Duration(p.Duration)*time.Second), maxDuration)
	meta := track.Meta{Title: part, Duration: duration}
	if meta.Title == "" {
		meta.Title = bvid
	}
	info := videoInfo{cid: p.CID}
	view, err := c.fetchView(ctx, viewOp, bvid, cookie)
	if err != nil {
		return meta, info, nil
	}
	title := cleanText(view.Title)
	if part != "" && part != title {
		if title != "" {
			title = title + " - " + part
		} else {
			title = part
		}
	}
	if title == "" {
		title = bvid
	}
	meta.Title = cleanText(title)
	meta.Artist = cleanText(view.Owner.Name)
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
