package bilibili

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"
)

type observer struct {
	mu    sync.Mutex
	calls []string
}

func (o *observer) observe(source, op string, _ time.Duration, err error) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, source+":"+op+":"+result)
}

func (o *observer) check(t *testing.T, want ...string) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.Equal(o.calls, want) {
		t.Fatalf("观察到的调用 = %v，期望 %v", o.calls, want)
	}
}

type credentialStore struct {
	mu      sync.Mutex
	value   string
	loadErr error
	saveErr error
	saves   []string
}

func (s *credentialStore) load(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.loadErr
}

func (s *credentialStore) save(_ context.Context, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.value = value
	s.saves = append(s.saves, value)
	return nil
}

func (s *credentialStore) set(t *testing.T, cookie, refreshToken string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = encodeCredential(cookie, refreshToken)
}

type recordedRequest struct {
	scheme  string
	host    string
	path    string
	query   string
	cookie  string
	referer string
	range_  string
}

// 把所有请求改写到 httptest 服务器，同时记录原来的地址和请求头。
type redirectTransport struct {
	target *url.URL
	rt     http.RoundTripper
	mu     sync.Mutex
	reqs   []recordedRequest
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.reqs = append(t.reqs, recordedRequest{
		scheme:  req.URL.Scheme,
		host:    req.URL.Host,
		path:    req.URL.Path,
		query:   req.URL.RawQuery,
		cookie:  req.Header.Get("Cookie"),
		referer: req.Header.Get("Referer"),
		range_:  req.Header.Get("Range"),
	})
	t.mu.Unlock()
	r := req.Clone(req.Context())
	r.URL.Scheme = t.target.Scheme
	r.URL.Host = t.target.Host
	return t.rt.RoundTrip(r)
}

func (t *redirectTransport) requests() []recordedRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.reqs)
}

func (t *redirectTransport) countRequests(path string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, r := range t.reqs {
		if r.path == path {
			n++
		}
	}
	return n
}

func serve(t *testing.T, routes map[string]http.HandlerFunc) (*Client, *redirectTransport, *observer, *credentialStore) {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range routes {
		mux.HandleFunc(path, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	rt := &redirectTransport{target: target, rt: &http.Transport{}}
	obs := &observer{}
	creds := &credentialStore{}
	return New(&http.Client{Transport: rt}, t.TempDir(), creds.load, creds.save, obs.observe), rt, obs, creds
}

func respondJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
