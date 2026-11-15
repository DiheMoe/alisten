package shortlink

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func allowLoopback(host string) bool { return host == "127.0.0.1" }

func never(string) bool { return false }

func TestResolve(t *testing.T) {
	var methods []string
	mux := http.NewServeMux()
	mux.HandleFunc("/short", func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		http.Redirect(w, r, "/mid", http.StatusFound)
	})
	mux.HandleFunc("/mid", func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		http.Redirect(w, r, "/playlist?id=1", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/playlist", func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		_, _ = w.Write([]byte("body"))
	})
	mux.HandleFunc("/six", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/five", http.StatusFound) })
	mux.HandleFunc("/five", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/f2", http.StatusFound) })
	mux.HandleFunc("/f2", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/f3", http.StatusFound) })
	mux.HandleFunc("/f3", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/f4", http.StatusFound) })
	mux.HandleFunc("/f4", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/f5", http.StatusFound) })
	mux.HandleFunc("/f5", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/playlist?id=2", http.StatusFound) })
	// 跳到同一个服务，只是主机名换成 localhost。这个地址能访问，但 localhost 不在允许的主机里。
	mux.HandleFunc("/evil", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+strings.Replace(r.Host, "127.0.0.1", "localhost", 1)+"/playlist?id=3", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := New(http.DefaultTransport, allowLoopback, never)
	ctx := context.Background()

	t.Run("正常跳转且只发 GET", func(t *testing.T) {
		methods = nil
		got, err := r.Resolve(ctx, srv.URL+"/short")
		if err != nil {
			t.Fatal(err)
		}
		if want := srv.URL + "/playlist?id=1"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if strings.Join(methods, ",") != "GET,GET,GET" {
			t.Fatalf("methods = %v", methods)
		}
	})
	t.Run("跳到认得的地址时不再请求它", func(t *testing.T) {
		methods = nil
		recognized := New(http.DefaultTransport, allowLoopback, func(link string) bool { return strings.Contains(link, "/playlist?") })
		got, err := recognized.Resolve(ctx, srv.URL+"/short")
		if err != nil {
			t.Fatal(err)
		}
		if want := srv.URL + "/playlist?id=1"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if strings.Join(methods, ",") != "GET,GET" {
			t.Fatalf("methods = %v，期望不请求 /playlist", methods)
		}
	})
	t.Run("五次跳转可以", func(t *testing.T) {
		got, err := r.Resolve(ctx, srv.URL+"/five")
		if err != nil {
			t.Fatal(err)
		}
		if want := srv.URL + "/playlist?id=2"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	t.Run("超过五次", func(t *testing.T) {
		methods = nil
		if _, err := r.Resolve(ctx, srv.URL+"/six"); err == nil {
			t.Fatal("want error")
		}
		if len(methods) != 0 {
			t.Fatalf("methods = %v，期望不请求第六次跳转指向的地址", methods)
		}
	})
	t.Run("跳到不允许的主机", func(t *testing.T) {
		methods = nil
		if _, err := r.Resolve(ctx, srv.URL+"/evil"); err == nil {
			t.Fatal("want error")
		}
		if len(methods) != 0 {
			t.Fatalf("methods = %v，期望不请求不允许的主机", methods)
		}
	})
	t.Run("起点不允许", func(t *testing.T) {
		if _, err := New(http.DefaultTransport, func(string) bool { return false }, never).Resolve(ctx, srv.URL+"/playlist"); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("非 http 协议", func(t *testing.T) {
		upstream := &anyScheme{}
		other := New(upstream, allowLoopback, never)
		if _, err := other.Resolve(ctx, "ftp://127.0.0.1/x"); err == nil || upstream.requests != 0 {
			t.Fatalf("Resolve 的错误 = %v，发出了 %d 个请求，期望不发请求就返回错误", err, upstream.requests)
		}
	})
}

// 什么协议的请求都照常响应的上游。标准库的 Transport 自己会拒绝 http 和 https 之外的协议，用它测不出 Resolve 有没有先检查。
type anyScheme struct{ requests int }

func (a *anyScheme) RoundTrip(*http.Request) (*http.Response, error) {
	a.requests++
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

// 不响应的上游：请求一直挂着，直到请求的 ctx 结束。
type hangingTransport struct{}

func (hangingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestResolveTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := New(hangingTransport{}, allowLoopback, never)
		start := time.Now()
		if _, err := r.Resolve(t.Context(), "http://127.0.0.1/slow"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("上游不响应时的错误 = %v，期望超过期限", err)
		}
		if elapsed := time.Since(start); elapsed != 5*time.Second {
			t.Errorf("等了 %v 才中止，期望 5 秒", elapsed)
		}
	})
}
