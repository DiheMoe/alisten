package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type output struct {
	path string
	data []byte
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("protocolgen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "仓库根目录")
	check := flags.Bool("check", false, "只检查生成物是否与 IDL 一致，不写入文件")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	outputs, err := generateFrom(filepath.Join(*root, "protocol", "idl.json"))
	if err != nil {
		report(stderr, "%v", err)
		return 1
	}
	status := 0
	for _, out := range outputs {
		path := filepath.Join(*root, filepath.FromSlash(out.path))
		if *check {
			old, err := os.ReadFile(path)
			switch {
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				report(stderr, "读取 %s：%v", out.path, err)
				status = 1
			case !bytes.Equal(old, out.data):
				report(stderr, "%s 不是最新的，运行 go run ./cmd/protocolgen 重新生成", out.path)
				status = 1
			}
			continue
		}
		if err := writeOutput(path, out.data); err != nil {
			report(stderr, "%v", err)
			return 1
		}
	}
	return status
}

func report(stderr io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, "protocolgen："+format+"\n", args...)
}

func generateFrom(path string) ([]output, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d, err := parseIDL(data)
	if err != nil {
		return nil, err
	}
	if err := d.checkHandwritten(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return generate(d)
}

// 生成函数只处理 check 接受过的 IDL，直接依赖它保证的结构，不再检查：引用都指向存在的类型（标量类型已经展开），数组都有 items，
// anyOf 是 [类型, {"type": "null"}]，allOf 的每个条件只按必填的枚举字段判断，需要解码函数的类型都已标记在 inbound 中。
func generate(d *idl) ([]output, error) {
	types, err := goTypes(d)
	if err != nil {
		return nil, err
	}
	decode, err := goDecode(d)
	if err != nil {
		return nil, err
	}
	api, err := openAPI(d)
	if err != nil {
		return nil, err
	}
	outputs := []output{
		{"protocol/types.gen.go", types},
		{"protocol/decode.gen.go", decode},
		{"protocol/openapi.gen.json", api},
		{"web/src/core/realtime/protocol.gen.ts", tsProtocol(d)},
		{"web/src/core/api/http.gen.ts", tsHTTP(d)},
	}
	if len(d.urlDefs()) > 0 {
		outputs = append(outputs, output{"web/src/core/realtime/protocol.gen.test.ts", tsValidatorTests(d)})
	}
	return outputs, nil
}

func writeOutput(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
