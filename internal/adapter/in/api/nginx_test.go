package api

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dihemoe/alisten/protocol"
)

// nginx 没有配置 client_max_body_size 时的默认值。
const nginxDefaultMaxBody = 1 << 20

type nginxBodySizes struct {
	max, buffer int64
}

func nginxSize(t *testing.T, value string) int64 {
	t.Helper()
	shift := 0
	switch {
	case strings.HasSuffix(value, "k"):
		shift = 10
	case strings.HasSuffix(value, "m"):
		shift = 20
	}
	n, err := strconv.ParseInt(strings.TrimRight(value, "km"), 10, 64)
	if err != nil {
		t.Fatalf("nginx 的大小 %q：%v", value, err)
	}
	return n << shift
}

// 只认 location 块和请求体的两个指令。返回值按 location 的修饰符加匹配式索引。
func nginxLocationBodySizes(t *testing.T, conf string) map[string]nginxBodySizes {
	t.Helper()
	sizes := map[string]nginxBodySizes{}
	var blocks []string
	for line := range strings.Lines(conf) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#"):
		case strings.HasSuffix(line, "{"):
			location, _ := strings.CutPrefix(strings.TrimSpace(strings.TrimSuffix(line, "{")), "location ")
			blocks = append(blocks, location)
		case line == "}":
			blocks = blocks[:len(blocks)-1]
		case len(blocks) > 0:
			name, value, _ := strings.Cut(strings.TrimSuffix(line, ";"), " ")
			s := sizes[blocks[len(blocks)-1]]
			switch name {
			case "client_max_body_size":
				s.max = nginxSize(t, value)
			case "client_body_buffer_size":
				s.buffer = nginxSize(t, value)
			default:
				continue
			}
			sizes[blocks[len(blocks)-1]] = s
		}
	}
	return sizes
}

// 请求体超过 nginx 的上限时，客户端收到的是 nginx 的 HTML 413，不是协议约定的 JSON 错误。超过缓冲区时请求体会写进临时文件。
// 所以 nginx 里这两项都不能小于 serve 给同一批接口定的上限。
func TestNginxBodySizesCoverServeLimits(t *testing.T) {
	conf, err := os.ReadFile("../../../../web/nginx/templates/alisten.d/proxy.conf.template")
	if err != nil {
		t.Fatal(err)
	}
	sizes := nginxLocationBodySizes(t, string(conf))
	tests := []struct {
		location string
		limit    int64
		routes   []protocol.Route
	}{
		{"^~ /api/", maxBodyBytes, nil},
		{`~ ^/api/me/playlists/[^/]+/(removals|copies|moves)$`, maxBatchBodyBytes,
			[]protocol.Route{protocol.RouteRemoveMyPlaylistItems.Route, protocol.RouteCopyMyPlaylistItems.Route, protocol.RouteMoveMyPlaylistItems.Route}},
		{"= /api/me/playlists/imports", maxImportBodyBytes, []protocol.Route{protocol.RouteImportMyPlaylist.Route}},
	}
	for _, tt := range tests {
		got, ok := sizes[tt.location]
		if !ok {
			t.Errorf("nginx 里没有 location %s", tt.location)
			continue
		}
		if got.max == 0 {
			got.max = nginxDefaultMaxBody
		}
		if got.max < tt.limit || got.buffer < tt.limit {
			t.Errorf("location %s 的 client_max_body_size = %d、client_body_buffer_size = %d，都不能小于 serve 的上限 %d", tt.location, got.max, got.buffer, tt.limit)
		}
		modifier, pattern, _ := strings.Cut(tt.location, " ")
		for _, route := range tt.routes {
			_, path, _ := strings.Cut(route.Pattern, " ")
			path = strings.ReplaceAll(path, "{id}", "0198c0de-0000-7000-8000-000000000001")
			matched := modifier == "=" && path == pattern
			if modifier == "~" {
				matched = regexp.MustCompile(pattern).MatchString(path)
			}
			if !matched {
				t.Errorf("%s 不归 location %s 管，它的请求体上限就对不上", route.Pattern, tt.location)
			}
		}
	}
}
