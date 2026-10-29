package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

const upstreamTimeout = 30 * time.Second

// 除回环、私有（包括 IPv6 唯一本地地址）、链路本地（包括云元数据服务的 169.254.169.254）、组播和未指定地址之外，
// 代理还拒绝连接这些地址段。
var blocked = []netip.Prefix{
	// 「本网络」地址段，不会出现在公网上；IsGlobalUnicast 只排除其中的 0.0.0.0。
	netip.MustParsePrefix("0.0.0.0/8"),
	// 运营商级 NAT 使用的共享地址段。
	netip.MustParsePrefix("100.64.0.0/10"),
	// IETF 协议分配、基准测试和保留地址段：不会出现在公网上，部分内网和云的网络把它们用作内部地址。
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// 文档专用地址段：只用于示例，不会出现在公网上，部分内网把它们用作内部地址。
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
	// 内嵌 IPv4 地址的 IPv6 地址段：NAT64、SIIT 网关和隧道会把它们转换成任意 IPv4 地址，包括私有地址。
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("::ffff:0:0:0/96"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"),
	// 已废弃的站点本地地址，部分网络仍把它当作内网。
	netip.MustParsePrefix("fec0::/10"),
}

func public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

type refusedError struct {
	host string
}

func (e *refusedError) Error() string { return fmt.Sprintf("%s 不是公网地址", e.host) }

// dialPublic 自己解析目标的域名，只连接解析出的公网地址：连接时不再解析域名，DNS 重绑定就无法绕过检查。
func dialPublic(resolver *net.Resolver) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var addrs []netip.Addr
		if a, err := netip.ParseAddr(host); err == nil {
			addrs = []netip.Addr{a}
		} else if addrs, err = resolver.LookupNetIP(ctx, "ip", host); err != nil {
			return nil, err
		}
		dialer := net.Dialer{Timeout: upstreamTimeout}
		var errs []error
		for _, a := range addrs {
			if !public(a) {
				continue
			}
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(a.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		if len(errs) == 0 {
			return nil, &refusedError{host: host}
		}
		return nil, errors.Join(errs...)
	}
}
