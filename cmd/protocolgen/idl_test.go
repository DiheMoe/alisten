package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestProtocolVersionIgnoresKeyOrderAndWhitespace(t *testing.T) {
	compact, err := protocolVersion([]byte(`{"a":{"x":1.0,"y":"中"},"b":[1,2]}`))
	if err != nil {
		t.Fatal(err)
	}
	if compact != "ce476f5a8616ebf9" {
		t.Errorf("版本 = %s，应为规范化文本 SHA-256 的前 16 个十六进制字符", compact)
	}
	reordered, err := protocolVersion([]byte("{\n  \"b\": [1, 2],\n  \"a\": {\"y\": \"\\u4e2d\", \"x\": 1.0}\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if reordered != compact {
		t.Errorf("只改了键的顺序和空白，版本却从 %s 变成了 %s", compact, reordered)
	}
	swapped, err := protocolVersion([]byte(`{"a":{"x":1.0,"y":"中"},"b":[2,1]}`))
	if err != nil {
		t.Fatal(err)
	}
	if swapped == compact {
		t.Error("数组元素的顺序变了，版本应随之改变")
	}
}

func object(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/$defs/" + name}
}

func minimalIDL() map[string]any {
	return map[string]any{
		"title": "Minimal",
		"$defs": map[string]any{
			"Empty":     object(map[string]any{}),
			"ErrorInfo": object(map[string]any{"message": map[string]any{"type": "string"}}, "message"),
		},
		"x-ws": map[string]any{
			"events": []any{map[string]any{"type": "error", "data": ref("ErrorInfo")}},
		},
	}
}

func defs(d map[string]any) map[string]any {
	return d["$defs"].(map[string]any)
}

func roomRequest(request map[string]any) map[string]any {
	return map[string]any{"type": "room.sync", "http": map[string]any{"method": "POST", "path": "/api/room/sync"}, "request": request, "response": ref("Empty")}
}

func roomBinding(method, path string, properties map[string]any, required ...string) map[string]any {
	m := roomRequest(object(properties, required...))
	m["http"] = map[string]any{"method": method, "path": path}
	return m
}

func bodylessEndpoint(name, method, path string) map[string]any {
	return map[string]any{"name": name, "method": method, "path": path, "auth": "none", "response": ref("Empty")}
}

func str(keywords ...any) map[string]any {
	s := map[string]any{"type": "string"}
	for i := 0; i+1 < len(keywords); i += 2 {
		s[keywords[i].(string)] = keywords[i+1]
	}
	return s
}

func condition(value string, then map[string]any) map[string]any {
	return map[string]any{
		"if":   map[string]any{"properties": map[string]any{"kind": map[string]any{"const": value}}, "required": []string{"kind"}},
		"then": then,
	}
}

func conditional(then map[string]any) map[string]any {
	s := object(map[string]any{"kind": ref("Kind"), "id": str()}, "kind", "id")
	s["allOf"] = []any{condition("link", then)}
	return s
}

func TestAllOfConditions(t *testing.T) {
	d := minimalIDL()
	defs(d)["Kind"] = map[string]any{"type": "string", "enum": []string{"link", "bilibili"}}
	defs(d)["Link"] = str("x-maxBytes", 2048)
	defs(d)["Track"] = map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"source": ref("Kind"), "id": str()},
		"required":   []string{"source", "id"},
		"allOf": []any{
			map[string]any{"if": map[string]any{"properties": map[string]any{"source": map[string]any{"const": "link"}}, "required": []string{"source"}}, "then": map[string]any{"properties": map[string]any{"id": ref("Link")}}},
			map[string]any{"if": map[string]any{"properties": map[string]any{"source": map[string]any{"const": "bilibili"}}, "required": []string{"source"}}, "then": map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^BV"}}}},
		},
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseIDL(data); err != nil {
		t.Fatalf("两个条件 = %v", err)
	}
}

func withKind(d map[string]any) {
	defs(d)["Kind"] = map[string]any{"type": "string", "enum": []string{"link", "netease"}}
}

func TestParseIDLRejects(t *testing.T) {
	tests := []struct {
		name   string
		change func(d map[string]any)
		want   string
	}{
		{"拼错的关键字", func(d map[string]any) {
			defs(d)["Empty"].(map[string]any)["minLenght"] = 1
		}, `unknown field "minLenght"`},
		{"引用不存在的类型", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": ref("Missing")}, "message")
		}, "引用的类型 #/$defs/Missing 不存在"},
		{"对象没有写 additionalProperties", func(d map[string]any) {
			delete(defs(d)["Empty"].(map[string]any), "additionalProperties")
		}, `"additionalProperties": false`},
		{"required 写了不存在的字段", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": map[string]any{"type": "string"}}, "message", "code")
		}, "required 中的 code 不在 properties 里"},
		{"请求中有数组", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(object(map[string]any{
				"ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "ids"))}
		}, "数组只能出现在响应和事件中"},
		{"请求引用的类型中有数组", func(d map[string]any) {
			defs(d)["Batch"] = object(map[string]any{
				"ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "ids")
			d["x-requests"] = []any{roomRequest(object(map[string]any{"batch": ref("Batch")}, "batch"))}
		}, "$defs.Batch.properties.ids：数组只能出现在响应和事件中"},
		{"生成的类型名重名", func(d map[string]any) {
			defs(d)["RoomSyncRequest"] = object(map[string]any{})
			d["x-requests"] = []any{roomRequest(object(map[string]any{}))}
		}, "生成的类型名 RoomSyncRequest 与其他类型重复"},
		{"oneOf 分支没有限定字段", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"a": map[string]any{"type": "string"}, "b": map[string]any{"type": "string"}},
				"oneOf":      []any{map[string]any{"required": []string{"a"}}, map[string]any{"required": []string{"b"}}},
			})}
		}, "分支应由 required、properties"},
		{"if 判断的不是枚举字段", func(d map[string]any) {
			s := object(map[string]any{"kind": map[string]any{"type": "string"}, "id": map[string]any{"type": "string"}}, "kind", "id")
			s["allOf"] = []any{condition("link", map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}})}
			defs(d)["Track"] = s
		}, "只支持按必填的枚举字段取值判断"},
		{"GET 接口带请求体", func(d map[string]any) {
			d["x-http"] = []any{map[string]any{
				"name": "listRooms", "method": "GET", "path": "/api/rooms", "auth": "none",
				"request": object(map[string]any{}), "response": ref("Empty"),
			}}
		}, "GET 请求没有请求体"},
		{"只有路径参数名不同的重复路由", func(d map[string]any) {
			d["x-http"] = []any{bodylessEndpoint("getRoom", "GET", "/api/rooms/{ref}"), bodylessEndpoint("findRoom", "GET", "/api/rooms/{id}")}
		}, "GET /api/rooms/{id} 与接口 getRoom 的路由重复"},
		{"接口放在房间内请求的路径下", func(d map[string]any) {
			d["x-http"] = []any{bodylessEndpoint("listQueue", "GET", "/api/room/queue")}
		}, `x-http[0]：path "/api/room/queue" 是 /api/room 或在它之下`},
		{"接口的路径是 /api/room", func(d map[string]any) {
			d["x-http"] = []any{bodylessEndpoint("getRoomState", "GET", "/api/room")}
		}, `x-http[0]：path "/api/room" 是 /api/room 或在它之下`},
		{"房间内请求没有 http", func(d map[string]any) {
			m := roomRequest(object(map[string]any{}))
			delete(m, "http")
			d["x-requests"] = []any{m}
		}, "x-requests[0]：缺少 http"},
		{"连接请求带了 http", func(d map[string]any) {
			d["x-ws"].(map[string]any)["requests"] = []any{map[string]any{
				"type": "ping", "http": map[string]any{"method": "POST", "path": "/api/room/ping"},
				"request": object(map[string]any{}), "response": ref("Empty"),
			}}
		}, "x-ws.requests[0]：只在 WebSocket 上使用的请求没有 http"},
		{"房间内请求的路径不在 /api/room 之下", func(d map[string]any) {
			d["x-requests"] = []any{roomBinding("GET", "/api/rooms/sync", map[string]any{})}
		}, `x-requests[0].http：path "/api/rooms/sync" 应是 /api/room 或在它之下`},
		{"房间内请求写明 200 的状态码", func(d map[string]any) {
			m := roomBinding("GET", "/api/room", map[string]any{})
			m["http"].(map[string]any)["status"] = 200
			d["x-requests"] = []any{m}
		}, "x-requests[0].http：status 只能是 201 或 204，缺省为 200"},
		{"与事件流的路由重复", func(d map[string]any) {
			d["x-requests"] = []any{roomBinding("GET", "/api/room/events", map[string]any{})}
		}, "x-requests[0].http：GET /api/room/events 与房间的事件流重复"},
		{"两个房间内请求的路由只有参数名不同", func(d map[string]any) {
			first := roomBinding("DELETE", "/api/room/queue/{entryId}", map[string]any{"entryId": str()}, "entryId")
			second := roomBinding("DELETE", "/api/room/queue/{id}", map[string]any{"id": str()}, "id")
			second["type"] = "queue.remove"
			d["x-requests"] = []any{first, second}
		}, "x-requests[1].http：DELETE /api/room/queue/{id} 与房间内请求 room.sync 的路由重复"},
		{"路径参数不是请求中的字段", func(d map[string]any) {
			d["x-requests"] = []any{roomBinding("DELETE", "/api/room/queue/{entryId}", map[string]any{})}
		}, "x-requests[0].http：路径参数 entryId 应是请求中必填的字符串字段"},
		{"路径参数是可选字段", func(d map[string]any) {
			d["x-requests"] = []any{roomBinding("DELETE", "/api/room/queue/{entryId}", map[string]any{"entryId": str()})}
		}, "x-requests[0].http：路径参数 entryId 应是请求中必填的字符串字段"},
		{"路径参数是整数字段", func(d map[string]any) {
			d["x-requests"] = []any{roomBinding("DELETE", "/api/room/queue/{entryId}", map[string]any{"entryId": map[string]any{"type": "integer"}}, "entryId")}
		}, "x-requests[0].http：路径参数 entryId 应是请求中必填的字符串字段"},
		{"路径参数重复", func(d map[string]any) {
			d["x-requests"] = []any{roomBinding("GET", "/api/room/{id}/{id}", map[string]any{"id": str()}, "id")}
		}, "x-requests[0].http：路径参数 id 重复"},
		{"GET 请求有对象字段", func(d map[string]any) {
			withKind(d)
			defs(d)["Track"] = object(map[string]any{"source": ref("Kind"), "id": str()}, "source", "id")
			d["x-requests"] = []any{roomBinding("GET", "/api/room", map[string]any{"track": ref("Track")}, "track")}
		}, "x-requests[0].http：GET 请求的字段 track 来自路径或查询串，应是字符串、整数或枚举"},
		{"204 的房间内请求有响应", func(d map[string]any) {
			m := roomBinding("POST", "/api/room/messages", map[string]any{})
			m["http"].(map[string]any)["status"] = 204
			m["response"] = object(map[string]any{"id": str()}, "id")
			d["x-requests"] = []any{m}
		}, "x-requests[0].http：204 的请求的 response 应为空对象，HTTP 不返回它"},
		{"无法编译的 pattern", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": map[string]any{"type": "string", "pattern": "("}}, "message")
		}, "pattern 无法编译"},
		{"整数枚举", func(d map[string]any) {
			defs(d)["Level"] = map[string]any{"type": "integer", "enum": []string{"1"}}
		}, "$defs.Level：枚举应为非空的字符串枚举"},
		{"枚举带了不支持的关键字", func(d map[string]any) {
			defs(d)["Level"] = map[string]any{"type": "string", "enum": []string{"low"}, "minLength": 1}
		}, "$defs.Level：这里不支持 minLength"},
		{"x-httpStatus 不是 HTTP 状态码", func(d map[string]any) {
			defs(d)["Code"] = map[string]any{"type": "string", "enum": []string{"NOT_FOUND"}, "x-httpStatus": map[string]int{"NOT_FOUND": 4040}}
		}, "x-httpStatus 中的 NOT_FOUND: 4040 不是枚举值或不是 HTTP 状态码"},
		{"字段名不是小驼峰式", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": str(), "Detail": str()}, "message")
		}, "$defs.ErrorInfo.properties.Detail：字段名应为小写字母开头的驼峰式"},
		{"字段生成相同的 Go 字段名", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": str(), "userID": str(), "userId": str()}, "message")
		}, "$defs.ErrorInfo.properties：userID 和 userId 生成相同的 Go 字段名 UserID"},
		{"请求中可以为 null 的字段", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(object(map[string]any{"note": map[string]any{"anyOf": []any{str(), map[string]any{"type": "null"}}}}, "note"))}
		}, "x-requests[0].request.properties.note：可以为 null 的字段只能出现在响应和事件中"},
		{"anyOf 的第二项不是 null", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": map[string]any{"anyOf": []any{str(), map[string]any{"type": "integer"}}}}, "message")
		}, `anyOf 只支持 [类型, {"type": "null"}]`},
		{"数组缺少 items", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": map[string]any{"type": "array"}}, "message")
		}, "$defs.ErrorInfo.properties.message：数组缺少 items"},
		{"请求中的布尔字段", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(object(map[string]any{"force": map[string]any{"type": "boolean"}}, "force"))}
		}, "x-requests[0].request.properties.force：请求中不支持 boolean"},
		{"不支持的类型", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": map[string]any{"type": "object"}}, "message")
		}, `$defs.ErrorInfo.properties.message：不支持的类型 "object"`},
		{"引用带了其他关键字", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": map[string]any{"$ref": "#/$defs/Empty", "minLength": 1}}, "message")
		}, "$defs.ErrorInfo.properties.message：这里不支持 minLength"},
		{"负的 maxLength", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": str("maxLength", -1)}, "message")
		}, "$defs.ErrorInfo.properties.message：minLength、maxLength 和 x-maxBytes 不能是负数"},
		{"minLength 大于 maxLength", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": str("minLength", 5, "maxLength", 4)}, "message")
		}, "$defs.ErrorInfo.properties.message：minLength 大于 maxLength"},
		{"minimum 大于 maximum", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": str(), "page": map[string]any{"type": "integer", "minimum": 2, "maximum": 1}}, "message", "page")
		}, "$defs.ErrorInfo.properties.page：minimum 大于 maximum"},
		{"oneOf 只有一个分支", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"a": str()},
				"oneOf":      []any{map[string]any{"required": []string{"a"}, "properties": map[string]any{"a": map[string]any{}}, "additionalProperties": false}},
			})}
		}, "x-requests[0].request：oneOf 至少要有两个分支"},
		{"字段不属于任何分支", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"a": str(), "b": str(), "c": str()},
				"oneOf": []any{
					map[string]any{"required": []string{"a"}, "properties": map[string]any{"a": map[string]any{}}, "additionalProperties": false},
					map[string]any{"required": []string{"b"}, "properties": map[string]any{"b": map[string]any{}}, "additionalProperties": false},
				},
			})}
		}, "x-requests[0].request：c 不属于 oneOf 的任何分支"},
		{"分支中的字段带了约束", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"a": str(), "b": str()},
				"oneOf": []any{
					map[string]any{"required": []string{"a"}, "properties": map[string]any{"a": str()}, "additionalProperties": false},
					map[string]any{"required": []string{"b"}, "properties": map[string]any{"b": map[string]any{}}, "additionalProperties": false},
				},
			})}
		}, "x-requests[0].request.oneOf[0]：properties 中的 a 应为 {}"},
		{"分支的可选字段不同", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"a": str(), "b": str(), "c": str()},
				"oneOf": []any{
					map[string]any{"required": []string{"a"}, "properties": map[string]any{"a": map[string]any{}, "c": map[string]any{}}, "additionalProperties": false},
					map[string]any{"required": []string{"b"}, "properties": map[string]any{"b": map[string]any{}}, "additionalProperties": false},
				},
			})}
		}, "x-requests[0].request.oneOf[1]：可选字段应与第一个分支相同（c），实际为（）"},
		{"分支的 required 有重复的字段", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"a": str(), "b": str()},
				"oneOf": []any{
					map[string]any{"required": []string{"a", "a"}, "properties": map[string]any{"a": map[string]any{}}, "additionalProperties": false},
					map[string]any{"required": []string{"b"}, "properties": map[string]any{"b": map[string]any{}}, "additionalProperties": false},
				},
			})}
		}, "x-requests[0].request.oneOf[0]：required 中的 a 重复"},
		{"分支中的字段是外层的必填字段", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"a": str(), "b": str()},
				"required":   []string{"a"},
				"oneOf": []any{
					map[string]any{"required": []string{"a"}, "properties": map[string]any{"a": map[string]any{}}, "additionalProperties": false},
					map[string]any{"required": []string{"b"}, "properties": map[string]any{"b": map[string]any{}}, "additionalProperties": false},
				},
			})}
		}, "x-requests[0].request.oneOf[0]：a 应是外层对象的可选字段"},
		{"allOf 元素缺少 then", func(d map[string]any) {
			withKind(d)
			s := conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}})
			delete(s["allOf"].([]any)[0].(map[string]any), "then")
			defs(d)["Track"] = s
		}, "$defs.Track.allOf[0]：要同时包含 if 和 then"},
		{"allOf 元素带了其他关键字", func(d map[string]any) {
			withKind(d)
			s := conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}})
			s["allOf"].([]any)[0].(map[string]any)["properties"] = map[string]any{}
			defs(d)["Track"] = s
		}, "$defs.Track.allOf[0]：这里不支持 properties"},
		{"空的 allOf", func(d map[string]any) {
			withKind(d)
			s := conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}})
			s["allOf"] = []any{}
			defs(d)["Track"] = s
		}, "$defs.Track：allOf 至少要有一个条件"},
		{"同一个字段的同一个取值有两个条件", func(d map[string]any) {
			withKind(d)
			s := conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}})
			s["allOf"] = append(s["allOf"].([]any), condition("link", map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^g"}}}))
			defs(d)["Track"] = s
		}, "$defs.Track.allOf[1]：与 allOf[0] 重复"},
		{"if 判断了两个字段", func(d map[string]any) {
			withKind(d)
			s := conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}})
			s["allOf"] = []any{map[string]any{
				"if":   map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "link"}, "id": map[string]any{"const": "x"}}, "required": []string{"kind", "id"}},
				"then": map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}},
			}}
			defs(d)["Track"] = s
		}, "$defs.Track.allOf[0].if：应写成"},
		{"if 中的 const 不是枚举的取值", func(d map[string]any) {
			withKind(d)
			s := conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}})
			s["allOf"] = []any{condition("qq", map[string]any{"properties": map[string]any{"id": map[string]any{"pattern": "^h"}}})}
			defs(d)["Track"] = s
		}, `$defs.Track.allOf[0].if："qq" 不是 #/$defs/Kind 的取值`},
		{"then 约束的不是必填的字符串字段", func(d map[string]any) {
			withKind(d)
			s := conditional(map[string]any{"properties": map[string]any{"title": map[string]any{"maxLength": 10}}})
			s["properties"].(map[string]any)["title"] = str()
			defs(d)["Track"] = s
		}, "$defs.Track.allOf[0].then.properties.title：then 只能约束必填的字符串字段"},
		{"then 中负的 x-maxBytes", func(d map[string]any) {
			withKind(d)
			defs(d)["Track"] = conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"x-maxBytes": -1}}})
		}, "$defs.Track.allOf[0].then.properties.id：minLength、maxLength 和 x-maxBytes 不能是负数"},
		{"事件的 data 引用了枚举", func(d map[string]any) {
			withKind(d)
			d["x-events"] = []any{map[string]any{"type": "queue.updated", "data": ref("Kind")}}
		}, "x-events[0].data：应引用对象类型"},
		{"事件的 data 引用带了其他关键字", func(d map[string]any) {
			d["x-events"] = []any{map[string]any{"type": "queue.updated", "data": map[string]any{"$ref": "#/$defs/Empty", "description": "空"}}}
		}, "x-events[0].data：这里不支持 description"},
		{"接口名与房间请求的 operationId 重复", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(object(map[string]any{}))}
			d["x-http"] = []any{bodylessEndpoint("roomSync", "GET", "/api/rooms")}
		}, `x-http[0]：接口名 "roomSync" 格式不对，或与其他接口、房间请求的 operationId 重复`},
		{"路径不在 /api 下", func(d map[string]any) {
			d["x-http"] = []any{bodylessEndpoint("listRooms", "GET", "/rooms")}
		}, `x-http[0]：method "GET" 或 path "/rooms" 不合法`},
		{"路径参数与 client 中的标识符重名", func(d map[string]any) {
			d["x-http"] = []any{bodylessEndpoint("getRoom", "GET", "/api/rooms/{send}")}
		}, "x-http[0]：路径参数 send 重复，或与生成的 TypeScript client 中的标识符重名"},
		{"同一个路径形状用了不同的参数名", func(d map[string]any) {
			d["x-http"] = []any{bodylessEndpoint("getRoom", "GET", "/api/rooms/{ref}"), bodylessEndpoint("deleteRoom", "DELETE", "/api/rooms/{id}")}
		}, "x-http[1]：路径 /api/rooms/{id} 与 /api/rooms/{ref} 只有参数名不同"},
		{"未知的凭证", func(d map[string]any) {
			e := bodylessEndpoint("listRooms", "GET", "/api/rooms")
			e["auth"] = "ticket"
			d["x-http"] = []any{e}
		}, "x-http[0]：auth 应为 none、member 或 admin"},
		{"写明 200 的状态码", func(d map[string]any) {
			e := bodylessEndpoint("listRooms", "GET", "/api/rooms")
			e["status"] = 200
			d["x-http"] = []any{e}
		}, "x-http[0]：status 只能是 201 或 204，缺省为 200"},
		{"不是 201 的接口带 Location", func(d map[string]any) {
			e := bodylessEndpoint("deleteRooms", "DELETE", "/api/rooms")
			e["status"], e["location"] = 204, true
			delete(e, "response")
			d["x-http"] = []any{e}
		}, "x-http[0]：location 只能用于 201 的接口"},
		{"204 的接口带 response", func(d map[string]any) {
			e := bodylessEndpoint("deleteRooms", "DELETE", "/api/rooms")
			e["status"] = 204
			d["x-http"] = []any{e}
		}, "x-http[0]：204 的接口没有 response"},
		{"不是 204 的接口没有 response", func(d map[string]any) {
			e := bodylessEndpoint("deleteRooms", "DELETE", "/api/rooms")
			delete(e, "response")
			d["x-http"] = []any{e}
		}, "x-http[0].response：缺少定义"},
		{"关闭码超出范围", func(d map[string]any) {
			d["x-ws"].(map[string]any)["closeCodes"] = []any{map[string]any{"name": "gone", "code": 999}}
		}, `x-ws.closeCodes[0]：名称 "gone" 或关闭码 999 不合法或重复`},
		{"类型名与请求的常量重名", func(d map[string]any) {
			d["x-requests"] = []any{roomRequest(object(map[string]any{}))}
			defs(d)["RequestRoomSync"] = object(map[string]any{})
		}, "生成的 Go 标识符 RequestRoomSync 重复：既是类型 RequestRoomSync，也是请求 room.sync 的常量"},
		{"路径参数是 JavaScript 的保留字", func(d map[string]any) {
			d["x-http"] = []any{bodylessEndpoint("getRoom", "GET", "/api/rooms/{default}")}
		}, "x-http[0]：路径参数 default 是 JavaScript 的保留字"},
		{"标量类型不是字符串或整数", func(d map[string]any) {
			defs(d)["Score"] = map[string]any{"type": "number"}
		}, `$defs.Score：类型应为对象、字符串枚举、"string" 或 "integer"`},
		{"then 引用了整数的标量类型", func(d map[string]any) {
			withKind(d)
			defs(d)["Count"] = map[string]any{"type": "integer", "minimum": 1}
			defs(d)["Track"] = conditional(map[string]any{"properties": map[string]any{"id": ref("Count")}})
		}, "$defs.Track.allOf[0].then.properties.id：只能引用字符串的标量类型"},
		{"then 中的引用带了其他关键字", func(d map[string]any) {
			withKind(d)
			defs(d)["Link"] = str("x-maxBytes", 2048)
			defs(d)["Track"] = conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"$ref": "#/$defs/Link", "minLength": 1}}})
		}, "$defs.Track.allOf[0].then.properties.id：这里不支持 minLength"},
		{"字段上的 x-url", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": str("x-url", true)}, "message")
		}, "$defs.ErrorInfo.properties.message：这里不支持 x-url"},
		{"then 中直接写 x-url", func(d map[string]any) {
			withKind(d)
			defs(d)["Track"] = conditional(map[string]any{"properties": map[string]any{"id": map[string]any{"x-url": true}}})
		}, "$defs.Track.allOf[0].then.properties.id：这里不支持 x-url"},
		{"整数的标量类型带了 x-url", func(d map[string]any) {
			defs(d)["Count"] = map[string]any{"type": "integer", "x-url": true}
		}, "$defs.Count：这里不支持 x-url"},
		{"x-url 与 pattern 同时使用", func(d map[string]any) {
			defs(d)["Link"] = str("x-url", true, "pattern", "^https://")
		}, "$defs.Link：x-url 只能是 true，并且不能与 pattern 同时使用"},
		{"单独的大小写变体", func(d map[string]any) {
			defs(d)["ErrorInfo"] = object(map[string]any{"message": map[string]any{"type": "string", "MAXLENGTH": 5}}, "message")
		}, `$defs.ErrorInfo.properties.message：键 "MAXLENGTH" 应写作 "maxLength"`},
		{"类型名与 TypeScript 中固定的类型重名", func(d map[string]any) {
			defs(d)["ServerMessage"] = object(map[string]any{})
		}, "生成的 TypeScript 标识符 ServerMessage 重复：既是生成器写出的 ServerMessage，也是类型 ServerMessage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := minimalIDL()
			tt.change(d)
			data, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseIDL(data)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("错误 = %v，应包含 %q", err, tt.want)
			}
		})
	}
}

func TestParseIDLRejectsNullAndDuplicateKeys(t *testing.T) {
	tests := map[string]string{
		`{"title": "T", "x-requests": [null]}`: "x-requests[0]：不能是 null",
		// encoding/json 按 Unicode 的大小写折叠匹配字段名，长 s（ſ）和 s 被视为同一个字符。
		`{"title": "T", "$defſ": {}}`: `顶层：键 "$defſ" 应写作 "$defs"`,
		`{"title": "T", "$defs": {"Empty": {"type": "object", "additionalProperties": false, "properties": {"a": {"type": "string"}, "a": {"type": "integer"}}}}}`: `$defs.Empty.properties：键 "a" 重复`,
	}
	for raw, want := range tests {
		if _, err := parseIDL([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseIDL(%s) 的错误 = %v，应包含 %q", raw, err, want)
		}
	}
}

func TestKeywordsCoverEverySchemaField(t *testing.T) {
	typ := reflect.TypeFor[schema]()
	for i := range typ.NumField() {
		field := typ.Field(i)
		keyword, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if keyword == "" {
			continue
		}
		var s schema
		v := reflect.ValueOf(&s).Elem().Field(i)
		switch v.Kind() {
		case reflect.String:
			v.SetString("x")
		case reflect.Pointer:
			v.Set(reflect.New(field.Type.Elem()))
		case reflect.Slice:
			v.Set(reflect.MakeSlice(field.Type, 0, 0))
		case reflect.Map:
			v.Set(reflect.MakeMap(field.Type))
		}
		if got := s.keywords(); !slices.Equal(got, []string{keyword}) {
			t.Errorf("只设置 %s 时 keywords() = %v，期望 [%s]", field.Name, got, keyword)
		}
	}
}

func TestParseIDLAcceptsRoutesDifferingByMethod(t *testing.T) {
	d := minimalIDL()
	d["x-http"] = []any{bodylessEndpoint("listRooms", "GET", "/api/rooms"), bodylessEndpoint("clearRooms", "DELETE", "/api/rooms")}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseIDL(data); err != nil {
		t.Fatal(err)
	}
}

func parseMinimal(t *testing.T, change func(d map[string]any)) *idl {
	t.Helper()
	d := minimalIDL()
	change(d)
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseIDL(data)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestGeneratedNamesAvoidHandwrittenCode(t *testing.T) {
	dir := filepath.Join("..", "..", "protocol")
	tests := []struct {
		name   string
		change func(d map[string]any)
		want   string
	}{
		{"类型", func(d map[string]any) {
			defs(d)["Message"] = object(map[string]any{})
		}, "生成的 Go 标识符 Message 与 ws.go 中手写的声明重名：它是类型 Message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := parseMinimal(t, tt.change).checkHandwritten(dir); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("错误 = %v，应包含 %q", err, tt.want)
			}
		})
	}
	if err := parseMinimal(t, func(map[string]any) {}).checkHandwritten(dir); err != nil {
		t.Errorf("最小的 IDL 不应与手写的代码重名：%v", err)
	}
}

var tsExport = regexp.MustCompile(`(?m)^export (?:type|interface|const|function) (\w+)`)

func TestTSNamesCoverGeneratedCode(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "idl.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseIDL(data)
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := generate(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range outputs {
		if !strings.HasSuffix(out.path, ".ts") {
			continue
		}
		for _, m := range tsExport.FindAllSubmatch(out.data, -1) {
			name := string(m[1])
			validator, ok := strings.CutPrefix(name, "is")
			if d.named[name] == nil && !slices.Contains(tsFixedNames, name) && (!ok || !slices.Contains(d.urlDefs(), validator)) {
				t.Errorf("%s 导出的 %s 既不是 IDL 中的类型和带 x-url 的标量类型的校验函数，也不在 tsFixedNames 中", out.path, name)
			}
		}
	}
}
