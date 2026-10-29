package track

import (
	"regexp"
	"strconv"
)

// 同一个视频只能有一种 ID，否则会重复下载：第 1 P 不带序号，序号不带前导零。
var bilibiliID = regexp.MustCompile(`^(BV[1-9A-HJ-NP-Za-km-z]{10})(?:/([2-9]|[1-9][0-9]{1,3}))?$`)

func BilibiliID(bvid string, page int) string {
	if page <= 1 {
		return bvid
	}
	return bvid + "/" + strconv.Itoa(page)
}

func ParseBilibiliID(id string) (bvid string, page int, ok bool) {
	m := bilibiliID.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	if m[2] == "" {
		return m[1], 1, true
	}
	page, _ = strconv.Atoi(m[2])
	return m[1], page, true
}

// id 必须是 ParseBilibiliID 能解析的 ID。
func BilibiliPageURL(id string) string {
	bvid, page, _ := ParseBilibiliID(id)
	u := "https://www.bilibili.com/video/" + bvid
	if page > 1 {
		u += "?p=" + strconv.Itoa(page)
	}
	return u
}
