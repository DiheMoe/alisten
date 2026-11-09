package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func errText(err error) string {
	if err == nil {
		return ""
	}
	var arg *ArgumentError
	if !errors.As(err, &arg) {
		return "不是 ArgumentError：" + err.Error()
	}
	return arg.Error()
}

func TestDecodeObject(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{`{"a":1}`, ""},
		{`{"z":1,"b":2}`, "data.b：未知字段"},
	}
	for _, tt := range tests {
		_, err := decodeObject(json.RawMessage(tt.raw), "data", "a")
		if got := errText(err); got != tt.want {
			t.Errorf("decodeObject(%s) = %q，期望 %q", tt.raw, got, tt.want)
		}
	}
}

func TestDecodeInteger(t *testing.T) {
	if n, err := decodeInteger(json.RawMessage(`42`), "x"); err != nil || n != 42 {
		t.Fatalf("decodeInteger = %d, %v", n, err)
	}
	for _, raw := range []string{`null`} {
		_, err := decodeInteger(json.RawMessage(raw), "x")
		if got := errText(err); got != "x：应为整数" {
			t.Errorf("decodeInteger(%s) = %q", raw, got)
		}
	}
}

func TestCheckOneOf(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{`{"track":1}`, ""},
		{`{"track":1,"requester":"张三"}`, ""},
		{`{"source":1,"keyword":1,"requester":"张三"}`, ""},
		{`{"requester":"张三"}`, "data：只能二选一"},
		{`{"track":1,"keyword":1,"requester":"张三"}`, "data：只能二选一"},
	}
	for _, tt := range tests {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.raw), &m); err != nil {
			t.Fatal(err)
		}
		err := checkOneOf(m, "data", "只能二选一", []string{"requester"}, []string{"track"}, []string{"source", "keyword"})
		if got := errText(err); got != tt.want {
			t.Errorf("checkOneOf(%s) = %q，期望 %q", tt.raw, got, tt.want)
		}
	}
}

func TestDecodeTrackArray(t *testing.T) {
	one := `{"source":"netease","id":"1"}`
	tests := []struct {
		body string
		want string
	}{
		{`{"tracks":[` + one + `]}`, ""},
		{`{"tracks":[]}`, "tracks：元素不能少于 1 个"},
		{`{"tracks":null}`, "tracks：应为数组"},
		{`{"tracks":[{"source":"netease"}]}`, "tracks[0].id：缺少必填字段"},
		{`{}`, "tracks：缺少必填字段"},
	}
	for _, tt := range tests {
		_, err := DecodeRemoveMyPlaylistItemsRequest([]byte(tt.body))
		if got := errText(err); got != tt.want {
			t.Errorf("%s = %q，期望 %q", tt.body, got, tt.want)
		}
	}
	for _, elem := range []string{one, "{}"} {
		tooMany := `{"tracks":[` + elem + strings.Repeat(","+elem, 1000) + `]}`
		if got := errText(func() error { _, err := DecodeRemoveMyPlaylistItemsRequest([]byte(tooMany)); return err }()); got != "tracks：元素不能超过 1000 个" {
			t.Errorf("1001 个 %s = %q，期望先按个数拒绝、不再解码元素", elem, got)
		}
	}
}
