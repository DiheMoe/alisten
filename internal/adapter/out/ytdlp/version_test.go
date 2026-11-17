package ytdlp

import (
	"os"
	"regexp"
	"testing"
)

// worker 镜像里的 yt-dlp 版本由 worker.Dockerfile 的 YTDLP_VERSION 决定。测试下载的版本和它不一致时，测试通过了，镜像里的 yt-dlp 却没有测过。
func TestYtdlpVersionMatchesWorkerImage(t *testing.T) {
	dockerfile, err := os.ReadFile("../../../../worker.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ARG YTDLP_VERSION=(\S+)$`).FindSubmatch(dockerfile)
	if m == nil {
		t.Fatal("worker.Dockerfile 里没有 ARG YTDLP_VERSION")
	}
	if got := string(m[1]); got != ytdlpVersion {
		t.Errorf("worker.Dockerfile 的 YTDLP_VERSION 是 %s，测试用的是 %s", got, ytdlpVersion)
	}
}
