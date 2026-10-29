package porttest

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	_ port.BilibiliSource        = (*Bilibili)(nil)
	_ port.BilibiliLinks         = (*Bilibili)(nil)
	_ port.BilibiliQRLoginSource = (*BilibiliQRLogin)(nil)
	_ port.Transcoder            = (*Transcoder)(nil)
	_ port.YouTubeCookies        = (*YouTubeCookies)(nil)
)

var ErrUnknownVideo = errors.New("音源中没有这个视频")

type Bilibili struct {
	mu       sync.Mutex
	videos   map[string]bilibiliVideo
	links    map[string]string
	rejects  map[string]string
	failure  error
	released int
}

type bilibiliVideo struct {
	meta     track.Meta
	audio    []byte
	cover    []byte
	coverExt string
	reject   string
}

func NewBilibili() *Bilibili {
	return &Bilibili{videos: map[string]bilibiliVideo{}, links: map[string]string{}, rejects: map[string]string{}}
}

// cover 为 nil 表示这个视频没有封面。
func (b *Bilibili) AddVideo(id string, meta track.Meta, audio, cover []byte, coverExt string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.videos[id] = bilibiliVideo{meta: meta, audio: audio, cover: cover, coverExt: coverExt}
}

// 元数据和下载都拒绝这个视频，比如视频不存在或需要付费。
func (b *Bilibili) RejectVideo(id, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.videos[id]
	v.reject = reason
	b.videos[id] = v
}

func (b *Bilibili) AddLink(link, id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.links[link] = id
}

// 是 B 站的链接但不是视频，比如番剧、直播和个人空间。
func (b *Bilibili) RejectLink(link, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rejects[link] = reason
}

func (b *Bilibili) Fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failure = err
}

// Released 是已经调用的 release 次数。
func (b *Bilibili) Released() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.released
}

func (b *Bilibili) Recognize(_ context.Context, link string) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure != nil {
		return "", false, b.failure
	}
	if reason, ok := b.rejects[link]; ok {
		return "", false, &port.RejectedError{Err: errors.New(reason)}
	}
	id, ok := b.links[link]
	return id, ok, nil
}

func (b *Bilibili) TrackMeta(_ context.Context, id string) (track.Meta, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure != nil {
		return track.Meta{}, b.failure
	}
	v, ok := b.videos[id]
	switch {
	case !ok:
		return track.Meta{}, &port.RejectedError{Err: ErrUnknownVideo}
	case v.reject != "":
		return track.Meta{}, &port.RejectedError{Err: errors.New(v.reject)}
	}
	return v.meta, nil
}

func (b *Bilibili) Download(_ context.Context, id string) (port.BilibiliDownload, func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure != nil {
		return port.BilibiliDownload{}, nil, b.failure
	}
	v, ok := b.videos[id]
	switch {
	case !ok:
		return port.BilibiliDownload{}, nil, &port.RejectedError{Err: ErrUnknownVideo}
	case v.reject != "":
		return port.BilibiliDownload{}, nil, &port.RejectedError{Err: errors.New(v.reject)}
	}
	d := port.BilibiliDownload{Meta: v.meta, Audio: LinkFile(v.audio)}
	if v.cover != nil {
		d.Cover, d.CoverExt = LinkFile(v.cover), v.coverExt
	}
	return d, b.markReleased, nil
}

func (b *Bilibili) markReleased() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.released++
}

type BilibiliQRLogin struct {
	qrLogins

	statusMu      sync.Mutex
	status        port.CredentialStatus
	statusFailure error
}

func NewBilibiliQRLogin() *BilibiliQRLogin {
	return &BilibiliQRLogin{qrLogins: qrLogins{logins: map[string]qrLogin{}}}
}

func (l *BilibiliQRLogin) CreateQRLogin(context.Context) (port.QRLoginSession, error) {
	key, expires, err := l.create("bilibili")
	if err != nil {
		return port.QRLoginSession{}, err
	}
	return port.QRLoginSession{Key: key, LoginURL: "https://bilibili.example/login?key=" + key, ExpiresAt: expires}, nil
}

func (l *BilibiliQRLogin) CheckQRLogin(_ context.Context, key string) (port.QRLoginResult, error) {
	return l.check("bilibili", key)
}

// account 为 nil 表示已保存登录信息但未识别到账号。
func (l *BilibiliQRLogin) SetCredential(account *port.SourceAccount) {
	l.statusMu.Lock()
	defer l.statusMu.Unlock()
	l.status, l.statusFailure = port.CredentialStatus{Set: true, Account: account}, nil
}

func (l *BilibiliQRLogin) FailStatus(err error) {
	l.statusMu.Lock()
	defer l.statusMu.Unlock()
	l.statusFailure = err
}

func (l *BilibiliQRLogin) CredentialStatus(context.Context) (port.CredentialStatus, error) {
	l.statusMu.Lock()
	defer l.statusMu.Unlock()
	if l.statusFailure != nil {
		return port.CredentialStatus{}, l.statusFailure
	}
	return l.status, nil
}

// 每次转码都返回构造时给的 mp3。
type Transcoder struct {
	mu       sync.Mutex
	mp3      []byte
	inputs   [][]byte
	failure  error
	released int
}

func NewTranscoder(mp3 []byte) *Transcoder {
	return &Transcoder{mp3: mp3}
}

func (t *Transcoder) Fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failure = err
}

// Released 是已经调用的 release 次数。
func (t *Transcoder) Released() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.released
}

// Inputs 是每次转码读到的输入，按调用的顺序，包括失败的调用。
func (t *Transcoder) Inputs() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.inputs)
}

func (t *Transcoder) ToMP3(_ context.Context, audio port.LinkFile) (port.LinkFile, func(), error) {
	f, err := audio.Open()
	if err != nil {
		return nil, nil, err
	}
	input, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return nil, nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inputs = append(t.inputs, input)
	if t.failure != nil {
		return nil, nil, t.failure
	}
	return LinkFile(t.mp3), t.markReleased, nil
}

func (t *Transcoder) markReleased() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.released++
}

type YouTubeCookies struct {
	mu      sync.Mutex
	cookies string
	status  port.YouTubeCookiesStatus
	failure error
}

func NewYouTubeCookies() *YouTubeCookies {
	return &YouTubeCookies{}
}

// Set 把 cookies 标记为已填写，updatedAt 是最近一次填写的时间。
func (y *YouTubeCookies) Set(updatedAt time.Time) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.status, y.failure = port.YouTubeCookiesStatus{Set: true, UpdatedAt: updatedAt}, nil
}

func (y *YouTubeCookies) Fail(err error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.failure = err
}

func (y *YouTubeCookies) SetCookies(_ context.Context, cookies string) error {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.failure != nil {
		return y.failure
	}
	y.cookies = cookies
	return nil
}

func (y *YouTubeCookies) Cookies() string {
	y.mu.Lock()
	defer y.mu.Unlock()
	return y.cookies
}

func (y *YouTubeCookies) Status(context.Context) (port.YouTubeCookiesStatus, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.failure != nil {
		return port.YouTubeCookiesStatus{}, y.failure
	}
	return y.status, nil
}
