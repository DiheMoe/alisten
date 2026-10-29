package port

import (
	"context"

	"github.com/dihemoe/alisten/internal/domain/track"
)

type BilibiliSource interface {
	// CoverURL 和 PageURL 留空：平台的图片有防盗链，封面在准备时下载存储；页面地址由调用方按 ID 拼出。
	TrackMeta(ctx context.Context, id string) (track.Meta, error)
	// 读完文件之后调用 release 删除它们；返回错误时没有要删除的文件。视频不存在、需要付费或地区受限时返回 *RejectedError。
	Download(ctx context.Context, id string) (d BilibiliDownload, release func(), err error)
}

// 与 LinkDownload 不同，Audio 是平台给的原始文件，可能还带着视频，要转成 mp3 才能分析和上传。
type BilibiliDownload struct {
	// 与 TrackMeta 的结果相同。
	Meta  track.Meta
	Audio LinkFile
	// 封面文件和它的扩展名（小写，不带点），没有封面时都为空。
	Cover    LinkFile
	CoverExt string
}

// 不是 B 站的链接时 ok 为 false、err 为 nil；是 B 站的链接但不是视频（番剧、直播、个人空间等）时返回 *RejectedError。
type BilibiliLinks interface {
	Recognize(ctx context.Context, link string) (id string, ok bool, err error)
}

type BilibiliQRLoginSource interface {
	CreateQRLogin(context.Context) (QRLoginSession, error)
	CheckQRLogin(context.Context, string) (QRLoginResult, error)
	// 是否已登录也由实现判断，凭据不离开适配器。
	CredentialStatus(context.Context) (CredentialStatus, error)
}

// 只读本地文件，不访问网络。读完 mp3 之后调用 release 删除它；返回错误时没有要删除的文件。
type Transcoder interface {
	ToMP3(ctx context.Context, audio LinkFile) (mp3 LinkFile, release func(), err error)
}
