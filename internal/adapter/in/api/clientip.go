package api

import (
	"net/http"
	"net/netip"
	"strings"
)

// 只有对端是回环、私有网段或链路本地地址的代理时才读 X-Forwarded-For，因为其他来源可以随意填写它。从最右端往左跳过受信任的代理，
// 第一个不受信任的地址就是客户端；全部受信任时取最左端的地址。受信任的代理只会写入合法的地址，
// 遇到不合法的值说明已经到了客户端自己填写的部分，此时改用对端地址。
func clientIP(r *http.Request) string {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	addr := peer.Addr().Unmap()
	if !trustedProxy(addr) {
		return addr.String()
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for hop := range strings.SplitSeq(v, ",") {
			hops = append(hops, strings.TrimSpace(hop))
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(hops[i])
		if err != nil {
			break
		}
		hop = hop.Unmap()
		if i == 0 || !trustedProxy(hop) {
			return hop.String()
		}
	}
	return addr.String()
}

func trustedProxy(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast()
}
