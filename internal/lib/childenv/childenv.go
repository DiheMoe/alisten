// Package childenv 为处理不可信输入的子进程挑选环境变量。
package childenv

import (
	"os"
	"slices"
	"strings"
)

var allowed = []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "XDG_CACHE_HOME", "SSL_CERT_FILE", "SSL_CERT_DIR"}

// Minimal 从本进程的环境里只留下子进程运行要用到的变量。本进程的环境里有数据库、Redis 和对象存储的密钥，
// 而 yt-dlp 和 ffmpeg 处理的是用户提交的任意链接和文件，所以其余的变量一律不传，代理设置也在其中。
func Minimal() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return !slices.Contains(allowed, name)
	})
}
