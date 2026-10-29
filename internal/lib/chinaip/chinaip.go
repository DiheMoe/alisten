// Package chinaip 让发往音源接口的请求带上随机的国内 IP：音源会限制来自国外的请求。
package chinaip

import (
	"fmt"
	"math/rand/v2"
	"net/http"
)

// 大陆运营商的地址段，音源把来自这些地址的请求当作国内请求。
var prefixes = [...][2]byte{
	{116, 255},
	{116, 228},
	{218, 192},
	{124, 0},
	{14, 132},
	{183, 14},
	{58, 14},
	{113, 116},
	{120, 230},
}

func Set(h http.Header) {
	p := prefixes[rand.IntN(len(prefixes))]
	ip := fmt.Sprintf("%d.%d.%d.%d", p[0], p[1], rand.IntN(256), rand.IntN(256))
	h.Set("X-Real-IP", ip)
	h.Set("X-Forwarded-For", ip)
}
