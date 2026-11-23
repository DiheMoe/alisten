// Package linkcookies 把数据库中保存的 YouTube cookies 写成临时文件供 yt-dlp 读取，因为 yt-dlp 只接受文件路径。
// 文件建在 worker 工作根目录下权限 0700 的独立临时目录中，下载结束后由调用方调用 release 删除整个目录，
// 这样 yt-dlp 运行期间写回的新文件也会一并清除。进程崩溃留下的残留目录由 worker 启动时的清理逻辑删除。
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

// 允许不带 scheme 的短链接，因为链接由用户提交，而且 yt-dlp 支持这种写法。
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
