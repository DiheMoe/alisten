package port

import (
	"context"
	"io"

	"github.com/dihemoe/alisten/internal/domain/track"
)

// Meta 只有链接本身给出的标题、歌手、专辑和时长，时长未知时为 0。
type LinkDownload struct {
	Meta track.Meta
	// 转成 mp3 的音频文件。
	Audio LinkFile
	// 封面文件和它的扩展名（小写，不带点），没有封面时都为空。
	Cover    LinkFile
	CoverExt string
}

type LinkFile interface {
	// 分析和上传会同时打开同一个文件，每次打开都从头读到同样的内容。
	Open() (io.ReadSeekCloser, error)
}

type LinkDownloader interface {
	// 读完文件之后调用 release 删除它们；返回错误时没有要删除的文件。链接不受支持或指向非公网地址时返回 *RejectedError。
	Download(ctx context.Context, link string) (d LinkDownload, release func(), err error)
}

// 用 yt-dlp 下载链接之前调用。这个链接不需要 cookies 时 ok 为 false；否则下载结束后调用 release，由实现删除文件。
type LinkCookies interface {
	CookieFile(ctx context.Context, link string) (path string, release func(), ok bool, err error)
}
