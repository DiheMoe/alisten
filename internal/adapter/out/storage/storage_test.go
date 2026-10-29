package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func put(t *testing.T, s *Storage, key, content, contentType string) {
	t.Helper()
	if err := s.Put(t.Context(), key, strings.NewReader(content), contentType); err != nil {
		t.Fatal(err)
	}
}

type anonymousResponse struct {
	status       int
	body         string
	contentType  string
	contentRange string
}

func anonymousGet(t *testing.T, url, rangeHeader string) anonymousResponse {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return anonymousResponse{status: resp.StatusCode, body: string(body), contentType: resp.Header.Get("Content-Type"), contentRange: resp.Header.Get("Content-Range")}
}

func TestEnsureBucketAllowsAnonymousReadsOfLinks(t *testing.T) {
	cfg := newConfig()
	s := New(cfg)
	if created, err := s.EnsureBucket(t.Context()); err != nil || !created {
		t.Fatalf("第一次检查 bucket = %v, %v，期望创建", created, err)
	}
	if created, err := s.EnsureBucket(t.Context()); err != nil || created {
		t.Fatalf("第二次检查 bucket = %v, %v，期望保持不变", created, err)
	}
	put(t, s, "link/abc/audio.mp3", "0123456789abcdef", "audio/mpeg")
	put(t, s, "other/secret.txt", "secret", "text/plain")
	base := cfg.Endpoint + "/" + cfg.Bucket

	got := anonymousGet(t, base+"/link/abc/audio.mp3", "")
	if want := (anonymousResponse{status: http.StatusOK, body: "0123456789abcdef", contentType: "audio/mpeg"}); got != want {
		t.Errorf("匿名读取 link/ 下的对象 = %+v，期望 %+v", got, want)
	}
	got = anonymousGet(t, base+"/link/abc/audio.mp3", "bytes=2-5")
	if want := (anonymousResponse{status: http.StatusPartialContent, body: "2345", contentType: "audio/mpeg", contentRange: "bytes 2-5/16"}); got != want {
		t.Errorf("带 Range 的匿名读取 = %+v，期望 %+v", got, want)
	}
	for _, path := range []string{"/other/secret.txt", "/", "/?list-type=2"} {
		if got := anonymousGet(t, base+path, ""); got.status != http.StatusForbidden {
			t.Errorf("匿名请求 %s = %d，期望 403：匿名只能读取 link/ 下的对象，不能列出对象", path, got.status)
		}
	}
}

// RustFS 对匿名请求也按查询参数 response-content-type 改写响应的 Content-Type（AWS 的 S3 不会）。链接曲目的音频和封面是
// 用户提交的内容，对象存储与页面同源时，被改成 text/html 的响应会在页面的源上运行脚本，只能靠入口为 /storage/ 的响应
// 添加 Content-Security-Policy: sandbox 和 X-Content-Type-Options: nosniff 来阻止。升级 RustFS 后这个测试如果失败，
// 先核对入口的这项要求是否仍然需要。
func TestAnonymousResponseContentTypeIsOverridable(t *testing.T) {
	cfg := newConfig()
	s := New(cfg)
	if _, err := s.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	const body = "<html><script>alert(1)</script></html>"
	put(t, s, "link/abc/cover.jpg", body, "image/jpeg")
	url := cfg.Endpoint + "/" + cfg.Bucket + "/link/abc/cover.jpg"

	if got, want := anonymousGet(t, url, ""), (anonymousResponse{status: http.StatusOK, body: body, contentType: "image/jpeg"}); got != want {
		t.Fatalf("匿名读取 = %+v，期望 %+v", got, want)
	}
	got := anonymousGet(t, url+"?response-content-type=text%2Fhtml", "")
	if want := (anonymousResponse{status: http.StatusOK, body: body, contentType: "text/html"}); got != want {
		t.Errorf("带 response-content-type 的匿名读取 = %+v，期望 %+v", got, want)
	}
}

func TestEnsureBucketLeavesExistingBucketUnchanged(t *testing.T) {
	cfg := newConfig()
	s := New(cfg)
	if _, err := s.client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatal(err)
	}
	if created, err := s.EnsureBucket(t.Context()); err != nil || created {
		t.Fatalf("检查已存在的 bucket = %v, %v，期望保持不变", created, err)
	}
	put(t, s, "link/abc/audio.mp3", "0123456789abcdef", "audio/mpeg")
	if got := anonymousGet(t, cfg.Endpoint+"/"+cfg.Bucket+"/link/abc/audio.mp3", ""); got.status != http.StatusForbidden {
		t.Errorf("匿名读取 = %d，期望 403：已存在的 bucket 不设置访问策略", got.status)
	}
}

func TestExistsReportsErrorsOtherThanNotFound(t *testing.T) {
	cfg := newConfig()
	s := New(cfg)
	if _, err := s.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	const key = "link/abc/audio.mp3"
	put(t, s, key, "mp3", "audio/mpeg")

	cfg.SecretAccessKey = "wrong-secret"
	if ok, err := New(cfg).Exists(t.Context(), key); err == nil || ok {
		t.Errorf("凭证不对时 Exists = %v, %v，期望返回错误", ok, err)
	}
}

func TestExistsAndDelete(t *testing.T) {
	s := New(newConfig())
	if _, err := s.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	const key = "link/abc/cover.jpg"
	if ok, err := s.Exists(t.Context(), key); err != nil || ok {
		t.Fatalf("上传之前 Exists = %v, %v", ok, err)
	}
	put(t, s, key, "jpeg", "image/jpeg")
	if ok, err := s.Exists(t.Context(), key); err != nil || !ok {
		t.Fatalf("上传之后 Exists = %v, %v", ok, err)
	}
	for range 2 {
		if err := s.Delete(t.Context(), key); err != nil {
			t.Fatalf("删除 = %v，期望对象不存在时同样成功", err)
		}
	}
	if ok, err := s.Exists(t.Context(), key); err != nil || ok {
		t.Errorf("删除之后 Exists = %v, %v", ok, err)
	}
}

// RustFS 上无法只让设置访问策略失败，所以用一个按方法应答的 HTTP 服务代替对象存储。
func fakeBucketServer(t *testing.T, policy http.HandlerFunc) (endpoint string, requests func() []string) {
	t.Helper()
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, isPolicy := r.URL.Query()["policy"]
		mu.Lock()
		received = append(received, r.Method+" "+r.URL.Path+map[bool]string{true: "?policy"}[isPolicy])
		mu.Unlock()
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && isPolicy:
			policy(w, r)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(received)
	}
}

func TestEnsureBucketRemovesBucketWithoutPolicy(t *testing.T) {
	endpoint, requests := fakeBucketServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
	})

	s := New(Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "alisten", AccessKeyID: accessKey, SecretAccessKey: secretKey})
	created, err := s.EnsureBucket(t.Context())
	if created || err == nil {
		t.Errorf("设置访问策略失败时 = %v, %v", created, err)
	}
	if want := []string{"HEAD /alisten", "PUT /alisten", "PUT /alisten?policy", "DELETE /alisten"}; !slices.Equal(requests(), want) {
		t.Errorf("请求依次为 %q，期望 %q：刚创建的 bucket 要删除", requests(), want)
	}
}

func TestEnsureBucketRemovesBucketWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	endpoint, requests := fakeBucketServer(t, func(_ http.ResponseWriter, r *http.Request) {
		// net/http 的服务端读完请求体之后才监视连接，客户端断开时 r.Context() 才会结束。
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	})

	s := New(Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "alisten", AccessKeyID: accessKey, SecretAccessKey: secretKey})
	created, err := s.EnsureBucket(ctx)
	if created || !errors.Is(err, context.Canceled) {
		t.Errorf("设置访问策略时被取消 = %v, %v", created, err)
	}
	if want := []string{"HEAD /alisten", "PUT /alisten", "PUT /alisten?policy", "DELETE /alisten"}; !slices.Equal(requests(), want) {
		t.Errorf("请求依次为 %q，期望 %q：取消之后刚创建的 bucket 仍要删除", requests(), want)
	}
}
