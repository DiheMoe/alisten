package linkcookies

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const savedCookies = "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tVISITOR_INFO1_LIVE\tfake-value\n"

func TestCookieFileSkipsNonYouTubeLinks(t *testing.T) {
	manager := New(t.TempDir(), func(context.Context) (string, error) {
		t.Error("非 YouTube 链接不应读取 cookies")
		return "", nil
	})
	for _, link := range []string{
		"https://www.bilibili.com/video/BV1xx411c7mD",
		"https://example.com/watch?v=dQw4w9WgXcQ",
		"https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ",
		"https://user@example.com/youtube",
		"/local/path",
		"",
	} {
		path, release, ok, err := manager.CookieFile(t.Context(), link)
		if ok || err != nil || path != "" || release != nil {
			t.Fatalf("%s 应跳过，得到 path=%q release=%v ok=%v err=%v", link, path, release != nil, ok, err)
		}
	}
}

func TestCookieFileRecognizesYouTubeHosts(t *testing.T) {
	root := t.TempDir()
	manager := New(root, func(context.Context) (string, error) { return savedCookies, nil })
	for _, link := range []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://YouTube.com/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ",
		"youtu.be/dQw4w9WgXcQ",
	} {
		path, release, ok, err := manager.CookieFile(t.Context(), link)
		if !ok || err != nil {
			t.Fatalf("%s 应识别为 YouTube 链接，得到 ok=%v err=%v", link, ok, err)
		}
		if !strings.HasPrefix(path, root) {
			t.Fatalf("cookies 文件应创建在工作目录下：%s", path)
		}
		release()
	}
}

func TestCookieFileWithoutSavedCookies(t *testing.T) {
	root := t.TempDir()
	for _, cookies := range []string{"", " \n\t"} {
		manager := New(root, func(context.Context) (string, error) { return cookies, nil })
		path, release, ok, err := manager.CookieFile(t.Context(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
		if ok || err != nil || path != "" || release != nil {
			t.Fatalf("cookies=%q 应跳过，得到 path=%q release=%v ok=%v err=%v", cookies, path, release != nil, ok, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("跳过时不应创建文件：%v", entries)
	}
}

func TestCookieFileCreatesFile(t *testing.T) {
	root := t.TempDir()
	manager := New(root, func(context.Context) (string, error) { return savedCookies, nil })
	path, release, ok, err := manager.CookieFile(t.Context(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	defer release()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != savedCookies {
		t.Fatalf("cookies 文件内容应与保存的内容一致，得到 %q", content)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("临时目录权限应为 0700，得到 %#o", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("cookies 文件权限应为 0600，得到 %#o", fileInfo.Mode().Perm())
	}
}

// yt-dlp 运行中会把更新后的 cookies 写回文件，release 连同回写内容一起删除且可以重复调用。
func TestReleaseRemovesRewrittenFile(t *testing.T) {
	root := t.TempDir()
	manager := New(root, func(context.Context) (string, error) { return savedCookies, nil })
	path, release, ok, err := manager.CookieFile(t.Context(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(path, []byte("# yt-dlp 回写的新 cookies\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release 后 cookies 文件应被删除，err=%v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release 后临时目录应被删除，err=%v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("release 后工作目录应为空：%v", entries)
	}
}

func TestCookieFileLoadFailure(t *testing.T) {
	root := t.TempDir()
	loadErr := errors.New("数据库连接失败")
	manager := New(root, func(context.Context) (string, error) { return "", loadErr })
	path, release, ok, err := manager.CookieFile(t.Context(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if ok || !errors.Is(err, loadErr) || path != "" || release != nil {
		t.Fatalf("读取失败得到 path=%q release=%v ok=%v err=%v，期望包装 %v", path, release != nil, ok, err, loadErr)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("失败时不应留下临时文件：%v", entries)
	}
}

func TestCookieFileMissingRoot(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "missing"), func(context.Context) (string, error) { return savedCookies, nil })
	_, _, ok, err := manager.CookieFile(t.Context(), "https://youtu.be/dQw4w9WgXcQ")
	if ok || err == nil {
		t.Fatalf("根目录不存在应返回错误，得到 ok=%v err=%v", ok, err)
	}
}
