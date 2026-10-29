package protocol

import (
	"encoding/json"
	"maps"
	"net/url"
	"slices"
	"strconv"
)

type Auth uint8

const (
	AuthNone Auth = iota
	AuthMember
	AuthTicket
	AuthAdmin
)

type Route struct {
	Pattern  string
	Auth     Auth
	Status   int
	Location bool
}

// 房间内请求的 HTTP 绑定。路径参数是请求中同名的字段；GET 和 DELETE 的其余字段来自查询串，其他方法的来自 JSON 请求体。
// 组装出的请求交给与 WebSocket 相同的解码函数校验。
type RoomRoute struct {
	Route
	// WebSocket 的请求类型。
	Type   string
	params []string
	query  map[string]queryKind
	body   bool
}

type queryKind uint8

const (
	queryString queryKind = iota
	queryInteger
)

// 空的请求体按 {} 处理：没有字段的请求可以不带请求体。
func (r RoomRoute) Decode(pathValue func(string) string, rawQuery string, body []byte) (any, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, invalid("", "查询串格式不正确")
	}
	m := map[string]json.RawMessage{}
	for _, name := range slices.Sorted(maps.Keys(query)) {
		kind, ok := r.query[name]
		switch {
		case !ok:
			return nil, invalid(name, "未知参数")
		case len(query[name]) > 1:
			return nil, invalid(name, "参数重复")
		case kind == queryInteger:
			n, err := strconv.ParseInt(query.Get(name), 10, 64)
			if err != nil {
				return nil, invalid(name, "应为整数")
			}
			m[name] = json.RawMessage(strconv.FormatInt(n, 10))
		default:
			m[name] = jsonString(query.Get(name))
		}
	}
	if r.body && len(body) > 0 {
		if json.Unmarshal(body, &m) != nil || m == nil {
			return nil, invalid("", "应为 JSON 对象")
		}
	}
	for _, name := range r.params {
		if _, ok := m[name]; ok {
			return nil, invalid(name, "未知字段")
		}
		m[name] = jsonString(pathValue(name))
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	data, _, err := decodeRoomRequest(r.Type, raw, "")
	if err != nil {
		return nil, err
	}
	return data, nil
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
