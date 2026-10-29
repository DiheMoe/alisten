// Package linkcookies 把数据库中保存的 YouTube cookies 写成 yt-dlp 需要的临时文件：
// yt-dlp 只接受文件路径，cookies 必须短暂落盘。文件写在 worker 工作根目录下权限 0700
// 的临时目录中，下载结束由调用方 release 删除；进程崩溃留下的残留由 worker 启动时的
// 清理逻辑删除。yt-dlp 运行中会把更新后的 cookies 写回文件，所以 release 删除整个目录。
package linkcookies

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dihemoe/alisten/internal/app/port"
)

type Manager struct {
	root string
	load func(context.Context) (string, error)
}

var _ port.LinkCookies = (*Manager)(nil)

// load 返回保存的 cookies，未填写时返回空字符串。
func New(root string, load func(context.Context) (string, error)) *Manager {
	return &Manager{root: root, load: load}
}

func (m *Manager) CookieFile(ctx context.Context, link string) (string, func(), bool, error) {
	if !isYouTube(link) {
		return "", nil, false, nil
	}
	cookies, err := m.load(ctx)
	if err != nil {
		return "", nil, false, fmt.Errorf("读取 YouTube cookies：%w", err)
	}
	if strings.TrimSpace(cookies) == "" {
		return "", nil, false, nil
	}
	dir, err := os.MkdirTemp(m.root, "cookies-")
	if err != nil {
		return "", nil, false, fmt.Errorf("创建 cookies 临时目录：%w", err)
	}
	path := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(path, []byte(cookies), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, false, fmt.Errorf("写入 cookies 文件：%w", err)
	}
	release := func() { _ = os.RemoveAll(dir) }
	return path, release, true, nil
}

// 链接由用户提交，yt-dlp 接受不带 scheme 的 YouTube 短链接。
func isYouTube(link string) bool {
	if !strings.Contains(link, "://") {
		link = "https://" + link
	}
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, domain := range []string{"youtube.com", "youtu.be", "youtube-nocookie.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
