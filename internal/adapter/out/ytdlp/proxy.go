package ytdlp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
)

// https 的请求经 CONNECT 建立隧道，http 的请求直接转发，两者的目标都只由 dial 连接。
type proxy struct {
	dial      func(ctx context.Context, network, address string) (net.Conn, error)
	server    *http.Server
	transport *http.Transport
	forward   *httputil.ReverseProxy
	url       string

	mu      sync.Mutex
	closed  bool
	refused bool
	// 隧道两端的连接在 Hijack 之后不再由 server 管理，代理关闭时要由这里关闭。
	tunnels map[*tunnel]struct{}
	running sync.WaitGroup
}

type tunnel struct {
	close func()
}

func startProxy(dial func(ctx context.Context, network, address string) (net.Conn, error)) (*proxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &proxy{dial: dial, url: "http://" + listener.Addr().String(), tunnels: map[*tunnel]struct{}{}}
	p.transport = &http.Transport{DialContext: p.connect, ResponseHeaderTimeout: upstreamTimeout}
	p.forward = &httputil.ReverseProxy{
		// 代理请求的地址就是要转发的绝对地址，原样转发，不加 X-Forwarded-For。
		Rewrite:      func(*httputil.ProxyRequest) {},
		Transport:    p.transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) { p.fail(w, err) },
	}
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: upstreamTimeout}
	go func() { _ = p.server.Serve(listener) }()
	return p, nil
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect:
		p.tunnel(w, r)
	case r.URL.IsAbs():
		p.forward.ServeHTTP(w, r)
	default:
		http.Error(w, "只接受代理请求", http.StatusBadRequest)
	}
}

func (p *proxy) connect(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := p.dial(ctx, network, address)
	var refused *refusedError
	if errors.As(err, &refused) {
		p.mu.Lock()
		p.refused = true
		p.mu.Unlock()
	}
	return conn, err
}

func (p *proxy) fail(w http.ResponseWriter, err error) {
	var refused *refusedError
	if errors.As(err, &refused) {
		http.Error(w, refused.Error(), http.StatusForbidden)
		return
	}
	http.Error(w, "连接目标失败", http.StatusBadGateway)
}

func (p *proxy) tunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := p.connect(r.Context(), "tcp", r.Host)
	if err != nil {
		p.fail(w, err)
		return
	}
	client, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	t := &tunnel{close: sync.OnceFunc(func() {
		_ = client.Close()
		_ = upstream.Close()
	})}
	if !p.hold(t) {
		t.close()
		return
	}
	defer p.release(t)
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(upstream, buffered)
		t.close()
	}()
	_, _ = io.Copy(client, upstream)
}

func (p *proxy) hold(t *tunnel) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.tunnels[t] = struct{}{}
	p.running.Add(1)
	return true
}

func (p *proxy) release(t *tunnel) {
	t.close()
	p.mu.Lock()
	delete(p.tunnels, t)
	p.mu.Unlock()
	p.running.Done()
}

func (p *proxy) wasRefused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refused
}

func (p *proxy) Close() {
	_ = p.server.Close()
	p.mu.Lock()
	p.closed = true
	for t := range p.tunnels {
		t.close()
	}
	p.mu.Unlock()
	p.running.Wait()
	p.transport.CloseIdleConnections()
}
