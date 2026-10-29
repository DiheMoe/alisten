package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "用生成结果覆盖 testdata/sample 中的期望文件")

var sampleDir = filepath.Join("testdata", "sample")

func sampleOutputs(t *testing.T) []output {
	t.Helper()
	outputs, err := generateFrom(filepath.Join(sampleDir, "protocol", "idl.json"))
	if err != nil {
		t.Fatal(err)
	}
	return outputs
}

func TestGenerateMatchesGolden(t *testing.T) {
	generated := map[string]bool{"protocol/idl.json": true}
	for _, out := range sampleOutputs(t) {
		generated[out.path] = true
		path := filepath.Join(sampleDir, filepath.FromSlash(out.path))
		if *update {
			if err := writeOutput(path, out.data); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s 缺少期望文件：%v", out.path, err)
			continue
		}
		if !bytes.Equal(out.data, want) {
			t.Errorf("%s 与期望不一致，%s", out.path, firstDiff(out.data, want))
		}
	}
	err := filepath.WalkDir(sampleDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, err := filepath.Rel(sampleDir, path)
		if err == nil && !generated[filepath.ToSlash(rel)] {
			t.Errorf("期望文件 %s 没有对应的生成物", filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func firstDiff(got, want []byte) string {
	g, w := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
	for i := range max(len(g), len(w)) {
		var a, b []byte
		if i < len(g) {
			a = g[i]
		}
		if i < len(w) {
			b = w[i]
		}
		if !bytes.Equal(a, b) {
			return fmt.Sprintf("第 %d 行：\n生成 %q\n期望 %q", i+1, a, b)
		}
	}
	return "只有行尾不同"
}
