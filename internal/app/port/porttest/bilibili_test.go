package porttest

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestBilibiliRecognize(t *testing.T) {
	b := NewBilibili()
	b.AddLink("https://www.bilibili.com/video/BV1xx411c7mD", "BV1xx411c7mD")
	b.AddLink("https://www.bilibili.com/video/BV1xx411c7mD?p=2", "BV1xx411c7mD/2")
	b.RejectLink("https://live.bilibili.com/6", "直播不是曲目")

	id, ok, err := b.Recognize(t.Context(), "https://www.bilibili.com/video/BV1xx411c7mD")
	if err != nil || !ok || id != "BV1xx411c7mD" {
		t.Fatalf("完整链接 = %q, %v, %v", id, ok, err)
	}
	id, ok, err = b.Recognize(t.Context(), "https://www.bilibili.com/video/BV1xx411c7mD?p=2")
	if err != nil || !ok || id != "BV1xx411c7mD/2" {
		t.Fatalf("分 P 链接 = %q, %v, %v", id, ok, err)
	}

	id, ok, err = b.Recognize(t.Context(), "https://example.com/video")
	if err != nil || ok || id != "" {
		t.Fatalf("不是 B 站的链接 = %q, %v, %v", id, ok, err)
	}

	_, _, err = b.Recognize(t.Context(), "https://live.bilibili.com/6")
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) || rejected.Error() != "直播不是曲目" {
		t.Fatalf("直播链接 = %v", err)
	}

	b.Fail(errors.New("请求失败"))
	if _, _, err = b.Recognize(t.Context(), "https://www.bilibili.com/video/BV1xx411c7mD"); err == nil {
		t.Fatal("识别失败时没有返回错误")
	}
}

func TestBilibiliTrackMeta(t *testing.T) {
	meta := track.Meta{Title: "一首歌", Artist: "UP 主", Duration: 3 * time.Minute}
	b := NewBilibili()
	b.AddVideo("BV1xx411c7mD", meta, []byte("aac"), nil, "")
	b.RejectVideo("BV1xx411c7mE", "视频需要付费")

	got, err := b.TrackMeta(t.Context(), "BV1xx411c7mD")
	if err != nil || got != meta {
		t.Fatalf("元数据 = %#v, %v", got, err)
	}

	var rejected *port.RejectedError
	if _, err = b.TrackMeta(t.Context(), "BV1xx411c7mE"); !errors.As(err, &rejected) || rejected.Error() != "视频需要付费" {
		t.Fatalf("付费视频 = %v", err)
	}
	if _, err = b.TrackMeta(t.Context(), "BV1xx411c7mF"); !errors.As(err, &rejected) {
		t.Fatalf("未知视频 = %v", err)
	}
}

func TestBilibiliDownload(t *testing.T) {
	meta := track.Meta{Title: "一首歌", Artist: "UP 主"}
	b := NewBilibili()
	b.AddVideo("BV1xx411c7mD", meta, []byte("aac"), []byte("jpeg"), "jpg")
	b.AddVideo("BV1xx411c7mE", meta, []byte("flv"), nil, "")

	d, release, err := b.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta != meta {
		t.Fatalf("下载的元数据 = %#v", d.Meta)
	}
	if got := readFile(t, d.Audio); got != "aac" {
		t.Fatalf("音频内容 = %q", got)
	}
	if got := readFile(t, d.Cover); got != "jpeg" || d.CoverExt != "jpg" {
		t.Fatalf("封面 = %q, %q", got, d.CoverExt)
	}
	if b.Released() != 0 {
		t.Fatalf("release 之前 Released = %d", b.Released())
	}
	release()
	if b.Released() != 1 {
		t.Fatalf("release 之后 Released = %d", b.Released())
	}

	d, release, err = b.Download(t.Context(), "BV1xx411c7mE")
	if err != nil {
		t.Fatal(err)
	}
	if d.Cover != nil || d.CoverExt != "" {
		t.Fatalf("没有封面 = %#v, %q", d.Cover, d.CoverExt)
	}
	release()
	if b.Released() != 2 {
		t.Fatalf("两次 release 之后 Released = %d", b.Released())
	}

	var rejected *port.RejectedError
	if _, release, err = b.Download(t.Context(), "BV1xx411c7mF"); !errors.As(err, &rejected) || release != nil {
		t.Fatalf("未知视频 = %v, release 非空 %v", err, release != nil)
	}

	b.Fail(errors.New("下载失败"))
	if _, release, err = b.Download(t.Context(), "BV1xx411c7mD"); err == nil || release != nil {
		t.Fatalf("下载失败 = %v, release 非空 %v", err, release != nil)
	}
}

func TestBilibiliQRLogin(t *testing.T) {
	l := NewBilibiliQRLogin()
	s, err := l.CreateQRLogin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if s.Key == "" || s.LoginURL == "" {
		t.Fatalf("会话 = %#v", s)
	}
	if !s.ExpiresAt.After(time.Now()) {
		t.Fatalf("过期时间 = %v", s.ExpiresAt)
	}

	r, err := l.CheckQRLogin(t.Context(), s.Key)
	if err != nil || r.Status != port.QRLoginWaiting {
		t.Fatalf("刚创建的会话 = %#v, %v", r, err)
	}

	l.SetResult(s.Key, port.QRLoginResult{Status: port.QRLoginScanned})
	if r, _ = l.CheckQRLogin(t.Context(), s.Key); r.Status != port.QRLoginScanned {
		t.Fatalf("扫码后 = %#v", r)
	}

	l.SetResult(s.Key, port.QRLoginResult{Status: port.QRLoginSuccess, Message: "已登录"})
	if r, _ = l.CheckQRLogin(t.Context(), s.Key); r.Status != port.QRLoginSuccess || r.Message != "已登录" {
		t.Fatalf("登录成功 = %#v", r)
	}

	if r, err = l.CheckQRLogin(t.Context(), "bilibili-999"); err != nil || r.Status != port.QRLoginExpired {
		t.Fatalf("未知会话 = %#v, %v", r, err)
	}

	l.Fail(errors.New("请求失败"))
	if _, err = l.CreateQRLogin(t.Context()); err == nil {
		t.Fatal("创建失败时没有返回错误")
	}
	if _, err = l.CheckQRLogin(t.Context(), s.Key); err == nil {
		t.Fatal("查询失败时没有返回错误")
	}
}

func TestBilibiliCredentialStatus(t *testing.T) {
	l := NewBilibiliQRLogin()
	s, err := l.CredentialStatus(t.Context())
	if err != nil || s.Set || s.Account != nil {
		t.Fatalf("未保存凭据 = %#v, %v", s, err)
	}

	name, vip := "UP 主", true
	l.SetCredential(&port.SourceAccount{ID: "322892", Name: &name, VIP: &vip, Verified: true})
	s, err = l.CredentialStatus(t.Context())
	if err != nil || !s.Set || s.Account == nil || s.Account.ID != "322892" || !s.Account.Verified {
		t.Fatalf("已登录 = %#v, %v", s, err)
	}

	l.SetCredential(nil)
	s, err = l.CredentialStatus(t.Context())
	if err != nil || !s.Set || s.Account != nil {
		t.Fatalf("已保存但未识别到账号 = %#v, %v", s, err)
	}

	l.FailStatus(errors.New("读取失败"))
	if _, err = l.CredentialStatus(t.Context()); err == nil {
		t.Fatal("读取失败时没有返回错误")
	}
}

func TestTranscoderToMP3(t *testing.T) {
	c := NewTranscoder([]byte("mp3"))
	mp3, release, err := c.ToMP3(t.Context(), LinkFile("aac"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, mp3); got != "mp3" {
		t.Fatalf("转码输出 = %q", got)
	}
	if c.Released() != 0 {
		t.Fatalf("release 之前 Released = %d", c.Released())
	}
	release()
	if c.Released() != 1 {
		t.Fatalf("release 之后 Released = %d", c.Released())
	}

	c.Fail(errors.New("转码失败"))
	if _, release, err = c.ToMP3(t.Context(), LinkFile("aac")); err == nil || release != nil {
		t.Fatalf("转码失败 = %v, release 非空 %v", err, release != nil)
	}
}

func TestTranscoderRecordsInputs(t *testing.T) {
	c := NewTranscoder([]byte("mp3"))
	_, release, err := c.ToMP3(t.Context(), LinkFile("aac"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	c.Fail(errors.New("转码失败"))
	if _, _, err := c.ToMP3(t.Context(), LinkFile("flv")); err == nil {
		t.Fatal("转码失败时没有返回错误")
	}
	if got := c.Inputs(); len(got) != 2 || string(got[0]) != "aac" || string(got[1]) != "flv" {
		t.Errorf("记下的输入 = %q，期望两次调用的输入，包括失败的那次", got)
	}
}

func TestStoreBilibiliCredential(t *testing.T) {
	s := New()
	if got, err := s.BilibiliCredential(t.Context()); err != nil || got != "" {
		t.Fatalf("初始 Bilibili 凭据 = %q, %v", got, err)
	}
	for _, value := range []string{`{"cookie":"SESSDATA=x","refresh_token":"y"}`, ""} {
		if err := s.SetBilibiliCredential(t.Context(), value); err != nil {
			t.Fatal(err)
		}
		if got, err := s.BilibiliCredential(t.Context()); err != nil || got != value {
			t.Errorf("Bilibili 凭据 = %q, %v，期望 %q", got, err, value)
		}
	}
}

func TestYouTubeCookiesStatus(t *testing.T) {
	y := NewYouTubeCookies()
	s, err := y.Status(t.Context())
	if err != nil || s.Set || !s.UpdatedAt.IsZero() {
		t.Fatalf("未填写 = %#v, %v", s, err)
	}

	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	y.Set(at)
	s, err = y.Status(t.Context())
	if err != nil || !s.Set || !s.UpdatedAt.Equal(at) {
		t.Fatalf("已填写 = %#v, %v", s, err)
	}

	y.Fail(errors.New("读取失败"))
	if _, err = y.Status(t.Context()); err == nil {
		t.Fatal("读取失败时没有返回错误")
	}
}

func readFile(t *testing.T, f port.LinkFile) string {
	t.Helper()
	r, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
