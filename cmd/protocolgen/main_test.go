package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleRoot(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sampleDir, "protocol", "idl.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := writeOutput(filepath.Join(root, "protocol", "idl.json"), data); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRunWritesThenChecks(t *testing.T) {
	root := sampleRoot(t)
	var stderr bytes.Buffer
	if code := run([]string{"-root", root}, &stderr); code != 0 {
		t.Fatalf("生成退出码 = %d，stderr：%s", code, &stderr)
	}
	outputs := sampleOutputs(t)
	for _, out := range outputs {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, out.data) {
			t.Errorf("写入的 %s 与生成结果不同", out.path)
		}
	}
	if code := run([]string{"-root", root, "-check"}, &stderr); code != 0 {
		t.Fatalf("刚生成完就检查，退出码 = %d，stderr：%s", code, &stderr)
	}

	stale := outputs[0].path
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(stale)), []byte("旧内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := run([]string{"-root", root, "-check"}, &stderr); code != 1 {
		t.Fatalf("生成物过期时退出码 = %d", code)
	}
	if !strings.Contains(stderr.String(), stale+" 不是最新的") {
		t.Errorf("stderr 没有指出过期的文件：%s", &stderr)
	}
}

func TestRunCheckReportsMissingAndUnreadableOutputs(t *testing.T) {
	root := sampleRoot(t)
	var stderr bytes.Buffer
	if code := run([]string{"-root", root}, &stderr); code != 0 {
		t.Fatalf("生成退出码 = %d，stderr：%s", code, &stderr)
	}
	target := filepath.Join(root, "protocol", "types.gen.go")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-root", root, "-check"}, &stderr); code != 1 || !strings.Contains(stderr.String(), "protocol/types.gen.go 不是最新的") {
		t.Fatalf("缺少生成物时退出码 = %d，stderr：%s", code, &stderr)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := run([]string{"-root", root, "-check"}, &stderr); code != 1 {
		t.Fatalf("生成物读不出来时退出码 = %d", code)
	}
	if got := stderr.String(); !strings.Contains(got, "读取 protocol/types.gen.go") || strings.Contains(got, "不是最新的") {
		t.Errorf("stderr = %s，期望报告读取的错误", got)
	}
}

func TestRunReportsInvalidIDL(t *testing.T) {
	root := t.TempDir()
	if err := writeOutput(filepath.Join(root, "protocol", "idl.json"), []byte(`{"title": "Broken", "$defs": {}}`)); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"-root", root}, &stderr); code != 1 {
		t.Fatalf("退出码 = %d", code)
	}
	if !strings.Contains(stderr.String(), "缺少对象类型 Empty") {
		t.Errorf("stderr = %s", &stderr)
	}
}
