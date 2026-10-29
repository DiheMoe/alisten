package protocol

import (
	"encoding/json"
	"errors"
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
