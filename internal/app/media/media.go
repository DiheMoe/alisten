package media

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/dihemoe/alisten/internal/domain/track"
)

// 链接和 B 站的歌在准备时下载，音频和封面存进对象存储，浏览器从对象存储读取。
func Stored(s track.Source) bool { return s == track.Link || s == track.Bilibili }

func Key(link string) string {
	sum := sha256.Sum256([]byte(link))
	return hex.EncodeToString(sum[:])
}

// 链接和 B 站的歌的对象都在 link/ 下：对象存储的匿名读取策略、nginx 和 Traefik 都只放行这里的对象。
func AudioObject(key string) string { return "link/" + key + "/audio.mp3" }

func CoverObject(key, ext string) string { return "link/" + key + "/cover." + ext }
