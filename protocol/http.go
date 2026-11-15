package protocol

import (
	"encoding/json"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
)

type Auth uint8

const (
	AuthNone Auth = iota
	AuthMember
	// 和 AuthMember 用同一个成员 token，但成员必须已经注册。
	AuthAccount
	AuthTicket
	AuthAdmin
)

type Route struct {
	Pattern string
	Auth    Auth
	Status  int
}

// 处理函数用不到请求的接口，请求类型是 NoRequest。这样的接口要么没有请求体，要么请求体是没有字段的对象，后者解码时只检查格式。
type NoRequest struct{}

// 生成器按 IDL 为每条接口选定下面三种类型之一，并填好请求的解码函数和路径参数。Req 是交给处理函数的请求。Resp 是响应体的类型，只用来约束处理函数的返回值。
type Endpoint[Req, Resp any] struct {
	Route
	decoder[Req]
	Params []PathParam
}

// 201 的接口，响应带 Location。
type LocatedEndpoint[Req, Resp any] struct {
	Route
	decoder[Req]
	Params []PathParam
}

// 204 的接口，没有响应体。
type NoContentEndpoint[Req any] struct {
	Route
	decoder[Req]
	Params []PathParam
}

// 路径参数的值要符合 IDL 中为它声明的类型。Type 是这个类型在 IDL 中的名字。
type PathParam struct {
	Name    string
	Type    string
	pattern *regexp.Regexp
}

// 格式不对的路径参数指向的资源一定不存在，所以调用方按 Type 对应的资源不存在处理。
type PathParamError struct {
	Name string
	Type string
}

func (e *PathParamError) Error() string {
	return "路径参数 " + e.Name + " 不符合类型 " + e.Type
}

func (e Endpoint[Req, Resp]) Decode(pathValue func(string) string, body []byte) (Req, error) {
	return e.decode(e.Params, pathValue, body)
}

func (e LocatedEndpoint[Req, Resp]) Decode(pathValue func(string) string, body []byte) (Req, error) {
	return e.decode(e.Params, pathValue, body)
}

func (e NoContentEndpoint[Req]) Decode(pathValue func(string) string, body []byte) (Req, error) {
	return e.decode(e.Params, pathValue, body)
}

type decoder[Req any] func(raw json.RawMessage, path string) (Req, error)

// 先解码请求体再查路径参数。这样格式不对的 ID 和格式正确却不存在的 ID 一样，请求体不合法时都先报请求体的错误。
func (d decoder[Req]) decode(params []PathParam, pathValue func(string) string, body []byte) (Req, error) {
	req, err := d(body, "")
	if err != nil {
		return req, err
	}
	for _, p := range params {
		if !p.pattern.MatchString(pathValue(p.Name)) {
			var zero Req
			return zero, &PathParamError{Name: p.Name, Type: p.Type}
		}
	}
	return req, nil
}

func noBody(json.RawMessage, string) (NoRequest, error) {
	return NoRequest{}, nil
}

func discard[T any](decode func(json.RawMessage, string) (T, error)) decoder[NoRequest] {
	return func(raw json.RawMessage, path string) (NoRequest, error) {
		_, err := decode(raw, path)
		return NoRequest{}, err
	}
}

// 房间内请求的 HTTP 绑定。路径参数是请求中同名的字段。GET 和 DELETE 的其余字段来自查询串，其他方法的其余字段来自 JSON 请求体。
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

// 空的请求体按 {} 处理，所以没有字段的请求可以不带请求体。
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
