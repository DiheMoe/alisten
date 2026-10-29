package ytdlp

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const ytdlpVersion = "2026.10.21"

var ytdlpArchives = map[string]struct{ name, executable, sha256 string }{
	"amd64": {"yt-dlp_linux.zip", "yt-dlp_linux", "dcddc44b6151ccd855a9a3c8fcdb00e897bb872ec9f4908b1b708b16eaaaa114"},
	"arm64": {"yt-dlp_linux_aarch64.zip", "yt-dlp_linux_aarch64", "d98a0aeb26b670aac94ee892a03a196f3f2cf0a81c3872186199c0c67ab36bde"},
}

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := installYtdlp()
	if err != nil {
		fmt.Fprintln(os.Stderr, "准备 yt-dlp：", err)
		return 1
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			fmt.Fprintf(os.Stderr, "测试需要 PATH 中有 %s：%v\n", tool, err)
			return 1
		}
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

func installYtdlp() (string, error) {
	archive, ok := ytdlpArchives[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		return "", fmt.Errorf("没有 %s/%s 的 yt-dlp", runtime.GOOS, runtime.GOARCH)
	}
	dir := filepath.Join(os.TempDir(), "alisten-yt-dlp-"+ytdlpVersion+"-"+runtime.GOARCH)
	if _, err := os.Stat(filepath.Join(dir, executable)); err == nil {
		return dir, nil
	}
	data, err := fetch("https://github.com/yt-dlp/yt-dlp/releases/download/" + ytdlpVersion + "/" + archive.name)
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != archive.sha256 {
		return "", fmt.Errorf("%s 的 SHA-256 不符", archive.name)
	}
	// 解压到同一目录下的临时目录，完成之后再改名，中途失败不会留下不完整的 yt-dlp。
	tmp, err := os.MkdirTemp(os.TempDir(), "alisten-yt-dlp-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := unzip(data, tmp); err != nil {
		return "", err
	}
	if err := os.Symlink(archive.executable, filepath.Join(tmp, executable)); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// 另一个测试进程先完成了安装。
		if _, statErr := os.Stat(filepath.Join(dir, executable)); statErr == nil {
			return dir, nil
		}
		return "", err
	}
	return dir, nil
}

func fetch(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s：%s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func unzip(data []byte, dir string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range r.File {
		if !filepath.IsLocal(f.Name) {
			return fmt.Errorf("压缩包中的路径 %q 不合法", f.Name)
		}
		path := filepath.Join(dir, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := extract(f, path); err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, f.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}
