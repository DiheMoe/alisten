package ytdlp

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublic(t *testing.T) {
	refused := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "fe80::1", "fc00::1", "fd12:3456::1",
		"100.64.0.1", "100.127.255.254", "0.0.0.0", "0.1.2.3", "::", "224.0.0.1", "ff02::1", "255.255.255.255",
		"64:ff9b::a00:1", "::ffff:100.64.0.1",
		"198.18.0.1", "198.19.255.254", "240.0.0.1", "255.255.255.254", "192.0.0.1", "192.0.0.255",
		"64:ff9b:1::a00:1", "::7f00:1", "2002:7f00:1::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2", "::ffff:0:a00:1",
		"fec0::1", "feff::1",
		"192.0.2.1", "192.0.2.255", "198.51.100.1", "203.0.113.254",
		"2001:db8::1", "2001:db8:ffff::1", "3fff::1", "3fff:fff:ffff::1",
	}
	for _, s := range refused {
		if public(netip.MustParseAddr(s)) {
			t.Errorf("%s 不应算作公网地址", s)
		}
	}
	// 紧挨着被拒绝的地址段的公网地址。
	for _, s := range []string{
		"8.8.8.8", "1.1.1.1", "100.128.0.1", "2606:4700:4700::1111", "::ffff:8.8.8.8",
		"198.17.255.255", "198.20.0.1", "223.255.255.254", "192.0.1.1", "2001:1::1", "2003::1", "64:ff9c::1",
		"192.0.3.1", "198.51.99.255", "198.51.101.1", "203.0.112.255", "203.0.114.1", "2001:db7:ffff::1", "2001:db9::1", "3ffe:ffff::1", "3fff:1000::1",
	} {
		if !public(netip.MustParseAddr(s)) {
			t.Errorf("%s 应算作公网地址", s)
		}
	}
}

func TestDialPublicRefusesPrivateTargets(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		conn, err := dialPublic(net.DefaultResolver)(t.Context(), "tcp", net.JoinHostPort(host, port))
		var refused *refusedError
		if !errors.As(err, &refused) {
			if conn != nil {
				_ = conn.Close()
			}
			t.Errorf("连接 %s = %v，期望拒绝", host, err)
		}
	}
}

// A 和 AAAA 的查询是同时发出的，所以一次解析就是各一次查询。
type dnsServer struct {
	addr    string
	answers func(qtype uint16, n int) []netip.Addr

	mu      sync.Mutex
	queries map[uint16]int
}

const (
	typeA    = 1
	typeAAAA = 28
)

func startDNS(t *testing.T, answers func(qtype uint16, n int) []netip.Addr) *dnsServer {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s := &dnsServer{addr: conn.LocalAddr().String(), answers: answers, queries: map[uint16]int{}}
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if reply := s.reply(buf[:n]); reply != nil {
				_, _ = conn.WriteTo(reply, from)
			}
		}
	}()
	return s
}

func (s *dnsServer) resolver() *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", s.addr)
	}}
}

// 回应只有一个问题的查询：问题原样放回，答案是 A 或 AAAA 记录，没有答案时是空的成功回应。
func (s *dnsServer) reply(query []byte) []byte {
	// 问题从第 12 个字节开始：域名是「长度加标签」的序列，以 0 结尾，之后是 2 字节的类型和 2 字节的类。
	end := 12
	for end < len(query) && query[end] != 0 {
		end += int(query[end]) + 1
	}
	end += 1 + 4
	if len(query) < 12 || end > len(query) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(query[end-4:])
	s.mu.Lock()
	s.queries[qtype]++
	addrs := s.answers(qtype, s.queries[qtype])
	s.mu.Unlock()

	reply := append(slices.Clone(query[:2]), 0x81, 0x80)
	reply = binary.BigEndian.AppendUint16(reply, 1)
	reply = binary.BigEndian.AppendUint16(reply, uint16(len(addrs)))
	reply = append(reply, 0, 0, 0, 0)
	reply = append(reply, query[12:end]...)
	for _, a := range addrs {
		typ := uint16(typeA)
		if a.Is6() {
			typ = typeAAAA
		}
		reply = append(reply, 0xC0, 0x0C)
		reply = binary.BigEndian.AppendUint16(reply, typ)
		reply = binary.BigEndian.AppendUint16(reply, 1)
		reply = binary.BigEndian.AppendUint32(reply, 0)
		reply = binary.BigEndian.AppendUint16(reply, uint16(len(a.AsSlice())))
		reply = append(reply, a.AsSlice()...)
	}
	return reply
}

// DNS 重绑定：域名第一次解析给出公网地址，之后的解析给出回环地址。
func TestDialPublicDoesNotResolveAgain(t *testing.T) {
	// Cloudflare 的公共 DNS，一直是公网地址。连接成功或失败都能看出连接的是哪个地址。
	public := netip.MustParseAddr("2606:4700:4700::1111")
	internal, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = internal.Close() })
	go func() {
		for {
			c, err := internal.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, err := net.SplitHostPort(internal.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	dns := startDNS(t, func(qtype uint16, n int) []netip.Addr {
		switch {
		case qtype == typeAAAA && n == 1:
			return []netip.Addr{public}
		case qtype == typeA && n > 1:
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, err := dialPublic(dns.resolver())(ctx, "tcp", net.JoinHostPort("rebind.test.", port))
	switch {
	case conn != nil:
		defer func() { _ = conn.Close() }()
		if got := conn.RemoteAddr().(*net.TCPAddr).AddrPort().Addr(); got != public {
			t.Errorf("连上了 %v，期望连接第一次解析出的 %v", got, public)
		}
	case !strings.Contains(err.Error(), "["+public.String()+"]"):
		t.Errorf("连接失败的错误 %q 没有提到第一次解析出的 %v，期望连接的是它", err, public)
	}
	dns.mu.Lock()
	defer dns.mu.Unlock()
	if dns.queries[typeA] != 1 || dns.queries[typeAAAA] != 1 {
		t.Errorf("域名被查询了 %v，期望 A 和 AAAA 各一次", dns.queries)
	}
}
