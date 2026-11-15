package redis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// 领域包里实现了 marker 方法的类型名，按方法名分组。
func domainTypes(t *testing.T) map[string][]string {
	t.Helper()
	const dir = "../../../domain/room"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string][]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			method, ok := decl.(*ast.FuncDecl)
			if !ok || method.Recv == nil || len(method.Recv.List) != 1 {
				continue
			}
			if receiver, ok := method.Recv.List[0].Type.(*ast.Ident); ok {
				found[method.Name.Name] = append(found[method.Name.Name], receiver.Name)
			}
		}
	}
	return found
}

// 领域里新增一种事件、回复、动作或输入而没有登记，编译能过，要到运行时发布才报错，房间还会因为发布失败被放弃。
func TestUnionsCoverDomainTypes(t *testing.T) {
	domain := domainTypes(t)
	// Command 单独编码，因为它里面还有一层动作。其余几种输入只由运行房间的实例自己产生，不跨实例。
	local := []string{"Command", "Prepared", "PrepareFailed", "Recommended", "Timer"}
	tests := []struct {
		marker     string
		registered []string
		local      []string
	}{
		{"event", slices.Collect(maps.Keys(events)), nil},
		{"result", slices.Collect(maps.Keys(results)), nil},
		{"action", slices.Collect(maps.Keys(actions)), nil},
		{"input", slices.Collect(maps.Keys(inputs)), local},
	}
	for _, tt := range tests {
		want := slices.DeleteFunc(slices.Clone(domain[tt.marker]), func(name string) bool { return slices.Contains(tt.local, name) })
		slices.Sort(want)
		slices.Sort(tt.registered)
		if len(want) == 0 || !slices.Equal(tt.registered, want) {
			t.Errorf("codec 登记的 %s 类型 = %v，领域里有 %v", tt.marker, tt.registered, want)
		}
	}
}
