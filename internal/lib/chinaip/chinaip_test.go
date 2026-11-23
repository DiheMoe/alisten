package chinaip

import (
	"net/http"
	"net/netip"
	"slices"
	"testing"
)

func TestSet(t *testing.T) {
	for range 32 {
		h := make(http.Header)
		Set(h)
		realIP, forwarded := h.Get("X-Real-IP"), h.Get("X-Forwarded-For")
		addr, err := netip.ParseAddr(realIP)
		if err != nil || !addr.Is4() {
			t.Fatalf("X-Real-IP = %q, err = %v，期望合法的 IPv4 地址", realIP, err)
		}
		if forwarded != realIP {
			t.Fatalf("X-Forwarded-For = %q，期望与 X-Real-IP %q 一致", forwarded, realIP)
		}
		raw := addr.As4()
		if !slices.Contains(prefixes[:], [2]byte{raw[0], raw[1]}) {
			t.Fatalf("IP %s 不在预置的国内地址段内", realIP)
		}
	}
}
