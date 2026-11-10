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
	mux.HandleFunc("/l", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	})
	mux.HandleFunc("/lx", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/l", http.StatusFound)
	})
	mux.HandleFunc("/five", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/f2", http.StatusFound) })
	mux.HandleFunc("/f2", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/f3", http.StatusFound) })
	mux.HandleFunc("/f3", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/f4", http.StatusFound) })
	mux.HandleFunc("/f4", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/f5", http.StatusFound) })
	mux.HandleFunc("/f5", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/playlist?id=2", http.StatusFound) })
	mux.HandleFunc("/evil", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:1/x", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := New(allowLoopback, never)
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
		recognized := New(allowLoopback, func(link string) bool { return strings.Contains(link, "/playlist?") })
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
		if _, err := r.Resolve(ctx, srv.URL+"/l"); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("跳到不允许的主机", func(t *testing.T) {
		if _, err := r.Resolve(ctx, srv.URL+"/evil"); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("起点不允许", func(t *testing.T) {
		if _, err := New(func(string) bool { return false }, never).Resolve(ctx, srv.URL+"/playlist"); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("非 http 协议", func(t *testing.T) {
		if _, err := r.Resolve(ctx, "file:///etc/passwd"); err == nil {
			t.Fatal("want error")
		}
	})
}

// 不响应的上游：请求一直挂着，直到请求的 ctx 结束。
type hangingTransport struct{}

func (hangingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestResolveTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := New(allowLoopback, never)
		r.client.Transport = hangingTransport{}
		start := time.Now()
		if _, err := r.Resolve(t.Context(), "http://127.0.0.1/slow"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("上游不响应时的错误 = %v，期望超过期限", err)
		}
		if elapsed := time.Since(start); elapsed != 5*time.Second {
			t.Errorf("等了 %v 才中止，期望 5 秒", elapsed)
		}
	})
}
