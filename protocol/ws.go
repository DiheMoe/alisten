package protocol

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type Message struct {
	Kind string `json:"kind"`
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	// 房间事件的 term 和 seq 都从 1 开始；响应和 error 事件不带编号，取 0 时不编码。
	Term  int64      `json:"term,omitempty"`
	Seq   int64      `json:"seq,omitempty"`
	Data  any        `json:"data"`
	Error *ErrorInfo `json:"error,omitempty"`
}

func Response(typ, id string, data any) Message {
	return Message{Kind: "response", Type: typ, ID: id, Data: data}
}

func Failure(typ, id string, err ErrorInfo) Message {
	return Message{Kind: "response", Type: typ, ID: id, Data: Empty{}, Error: &err}
}

func RoomEvent(typ string, term, seq int64, data any) Message {
	return Message{Kind: "event", Type: typ, Term: term, Seq: seq, Data: data}
}

func ErrorEvent(err ErrorInfo) Message {
	return Message{Kind: "event", Type: EventError, Data: err}
}

type Request struct {
	Type string
	ID   string
	Data any
}

// DecodeRequest 返回错误时，ID 为空表示这一帧无法对应到请求，应发送 error 事件；否则回复这个请求的失败响应。
func DecodeRequest(frame []byte) (Request, error) {
	var env map[string]json.RawMessage
	if json.Unmarshal(frame, &env) != nil || env == nil {
		return Request{}, invalid("", "消息必须是 JSON 对象")
	}
	var kind, id, typ string
	if json.Unmarshal(env["kind"], &kind) != nil || kind != "request" {
		return Request{}, invalid("kind", `应为 "request"`)
	}
	if json.Unmarshal(env["id"], &id) != nil || utf8.RuneCountInString(id) < 1 || utf8.RuneCountInString(id) > 128 {
		return Request{}, invalid("id", "应为 1–128 个字符的字符串")
	}
	if strings.ContainsRune(id, 0) {
		return Request{}, invalid("id", "不能包含 U+0000")
	}
	if json.Unmarshal(env["type"], &typ) != nil || typ == "" {
		return Request{}, invalid("type", "应为非空字符串")
	}
	req := Request{Type: typ, ID: id}
	if err := rejectUnknown(env, "", "kind", "type", "id", "data"); err != nil {
		return req, err
	}
	if err := requireFields(env, "", "data"); err != nil {
		return req, err
	}
	data, known, err := decodeConnectionRequest(typ, env["data"], "data")
	if !known {
		data, known, err = decodeRoomRequest(typ, env["data"], "data")
	}
	if !known {
		return req, invalid("type", "未知的请求类型")
	}
	if err != nil {
		return req, err
	}
	req.Data = data
	return req, nil
}
