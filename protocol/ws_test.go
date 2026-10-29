package protocol

import (
	"strings"
	"testing"
)

func TestDecodeRequestEnvelope(t *testing.T) {
	tests := []struct {
		name    string
		frame   string
		wantID  string
		wantErr string
	}{
		{"kind 不对", `{"kind":"response","type":"room.sync","id":"r1","data":{}}`, "", `kind：应为 "request"`},
		{"id 太长", `{"kind":"request","type":"room.sync","id":"` + strings.Repeat("a", 129) + `","data":{}}`, "", "id：应为 1–128 个字符的字符串"},
		{"id 含 U+0000", `{"kind":"request","type":"room.sync","id":"a\u0000b","data":{}}`, "", "id：不能包含 U+0000"},
		{"缺少 type", `{"kind":"request","id":"r1","data":{}}`, "", "type：应为非空字符串"},
		{"多余的字段", `{"kind":"request","type":"room.sync","id":"r1","data":{},"term":1}`, "r1", "term：未知字段"},
		{"缺少 data", `{"kind":"request","type":"room.sync","id":"r1"}`, "r1", "data：缺少必填字段"},
		{"data 不合法", `{"kind":"request","type":"queue.like","id":"r1","data":{}}`, "r1", "data.entryId：缺少必填字段"},
		{"id 按字符计数", `{"kind":"request","type":"room.sync","id":"` + strings.Repeat("歌", 128) + `","data":{}}`, strings.Repeat("歌", 128), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := DecodeRequest([]byte(tt.frame))
			if got := errText(err); got != tt.wantErr {
				t.Errorf("错误 = %q，期望 %q", got, tt.wantErr)
			}
			if req.ID != tt.wantID {
				t.Errorf("ID = %q，期望 %q", req.ID, tt.wantID)
			}
		})
	}
}

func decodeData(typ, data string) error {
	_, err := DecodeRequest([]byte(`{"kind":"request","type":"` + typ + `","id":"r1","data":` + data + `}`))
	return err
}

func TestRequestValidation(t *testing.T) {
	longLink := "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/"))
	tests := []struct {
		name string
		typ  string
		data string
		want string
	}{
		{"点歌：链接超过 2048 字节", "queue.add", `{"track":{"source":"link","id":"` + longLink + `a"}}`, "data.track.id：不能超过 2048 字节"},
		{"点歌：链接不是 http", "queue.add", `{"track":{"source":"link","id":"ftp://example.com/a"}}`, "data.track.id：应为 http 或 https 的链接"},
		{"点歌：未知来源", "queue.add", `{"track":{"source":"apple","id":"1"}}`, "data.track.source：取值应为 netease、qq、bilibili、link 之一"},
		{"点歌：关键词 500 个字符", "queue.add", `{"source":"qq","keyword":"` + strings.Repeat("歌", 500) + `"}`, ""},
		{"点歌：关键词超过 500 个字符", "queue.add", `{"source":"qq","keyword":"` + strings.Repeat("歌", 501) + `"}`, "data.keyword：长度不能超过 500 个字符"},
		{"点歌：关键词含 NUL", "queue.add", `{"source":"qq","keyword":"晴\u0000天"}`, "data.keyword：不能包含 U+0000"},
		{"点歌：两种写法混用", "queue.add", `{"source":"qq","keyword":"晴天","track":{"source":"qq","id":"1"}}`, "data：只能提供以下字段组合之一：track；source、keyword"},
		{"点歌：只有来源", "queue.add", `{"source":"qq"}`, "data：只能提供以下字段组合之一：track；source、keyword"},
		{"点歌：指定曲目时代填 80 个字符的点歌人", "queue.add", `{"track":{"source":"qq","id":"1"},"requester":"` + strings.Repeat("名", 80) + `"}`, ""},
		{"点歌：点歌人为空", "queue.add", `{"source":"qq","keyword":"晴天","requester":""}`, "data.requester：长度不能少于 1 个字符"},
		{"点歌：点歌人超过 80 个字符", "queue.add", `{"track":{"source":"qq","id":"1"},"requester":"` + strings.Repeat("名", 81) + `"}`, "data.requester：长度不能超过 80 个字符"},
		{"搜索：每页 100 条", "search.tracks", `{"source":"netease","keyword":"a","page":1,"pageSize":100}`, ""},
		{"搜索：每页超过 100 条", "search.tracks", `{"source":"netease","keyword":"a","page":1,"pageSize":101}`, "data.pageSize：不能大于 100"},
		{"搜索：页码从 1 开始", "search.playlists", `{"source":"netease","keyword":"a","page":0,"pageSize":10}`, "data.page：不能小于 1"},
		{"搜索：关键词为空", "search.tracks", `{"source":"netease","keyword":"","page":1,"pageSize":10}`, "data.keyword：长度不能少于 1 个字符"},
		{"聊天：长度不受限制", "chat.send", `{"content":"` + strings.Repeat("歌", 1000) + `"}`, ""},
		{"心跳：时间必须是整数", "ping", `{"clientTimeMs":1.5}`, "data.clientTimeMs：应为整数"},
		{"播放模式：轮流播放", "settings.setPlayMode", `{"playMode":"rotation"}`, ""},
		{"播放模式：未知的模式", "settings.setPlayMode", `{"playMode":"shuffle"}`, "data.playMode：取值应为 sequential、random、rotation 之一"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errText(decodeData(tt.typ, tt.data)); got != tt.want {
				t.Errorf("错误 = %q，期望 %q", got, tt.want)
			}
		})
	}
}
