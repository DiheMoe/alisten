package ytdlp

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func direct(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

func startTestProxy(t *testing.T, dial func(context.Context, string, string) (net.Conn, error)) *proxy {
	t.Helper()
	p, err := startProxy(dial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func viaProxy(t *testing.T, p *proxy, server *httptest.Server) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse(p.url)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	if server.TLS != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func get(t *testing.T, client *http.Client, url string) (int, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

func TestProxyRefusesPrivateTargets(t *testing.T) {
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "内网")
	})
	plain, secure := httptest.NewServer(handler), httptest.NewTLSServer(handler)
	defer plain.Close()
	defer secure.Close()

	p := startTestProxy(t, dialPublic(net.DefaultResolver))
	if status, _, err := get(t, viaProxy(t, p, plain), plain.URL+"/a.mp3"); err != nil || status != http.StatusForbidden {
		t.Errorf("经代理请求 http 的内网地址 = %d, %v，期望 403", status, err)
	}
	if _, _, err := get(t, viaProxy(t, p, secure), secure.URL+"/a.mp3"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("经代理请求 https 的内网地址 = %v，期望隧道被拒绝", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("内网的服务收到了 %d 个请求", n)
	}
	if !p.wasRefused() {
		t.Error("代理应记下拒绝过的目标")
	}
}

func TestProxyForwardsRequests(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "" {
			t.Errorf("转发的请求带有 X-Forwarded-For：%q", r.Header.Get("X-Forwarded-For"))
		}
		_, _ = io.WriteString(w, "音频 "+r.URL.Path)
	})
	plain, secure := httptest.NewServer(handler), httptest.NewTLSServer(handler)
	defer plain.Close()
	defer secure.Close()

	p := startTestProxy(t, direct)
	for _, server := range []*httptest.Server{plain, secure} {
		if status, body, err := get(t, viaProxy(t, p, server), server.URL+"/a.mp3"); err != nil || status != http.StatusOK || body != "音频 /a.mp3" {
			t.Errorf("经代理请求 %s = %d %q %v", server.URL, status, body, err)
		}
	}
	if p.wasRefused() {
		t.Error("没有拒绝过目标")
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.url+"/a.mp3", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("直接请求代理 = %d，期望 400：代理只接受代理请求", resp.StatusCode)
	}
}

func TestProxyCloseEndsTunnels(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upstream.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := upstream.Accept(); err == nil {
			accepted <- c
		}
	}()

	p, err := startProxy(direct)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", strings.TrimPrefix(p.url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if _, err := io.WriteString(client, "CONNECT "+upstream.Addr().String()+" HTTP/1.1\r\nHost: "+upstream.Addr().String()+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len("HTTP/1.1 200 Connection established\r\n\r\n"))
	if _, err := io.ReadFull(client, reply); err != nil || !strings.HasPrefix(string(reply), "HTTP/1.1 200") {
		t.Fatalf("CONNECT 的回复 = %q, %v", reply, err)
	}
	server := <-accepted
	defer func() { _ = server.Close() }()

	closed := make(chan struct{})
	go func() {
		p.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("有隧道时代理 5 秒内没有关闭")
	}
	if err := server.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("代理关闭之后目标一端的读取 = %v，期望连接已关闭", err)
	}
}
