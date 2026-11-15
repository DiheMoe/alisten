package protocol

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const someUUID = "01a15778-1911-7a28-8b2e-e08c2a25f388"

// 测请求体时用，每个路径参数都取合法的值。
func validPath(string) string {
	return someUUID
}

func TestHTTPRequestValidation(t *testing.T) {
	decode := map[string]func([]byte) error{
		"updateMe":    func(b []byte) error { _, err := RouteUpdateMe.Decode(validPath, b); return err },
		"createRoom":  func(b []byte) error { _, err := RouteCreateRoom.Decode(validPath, b); return err },
		"adminCreate": func(b []byte) error { _, err := RouteAdminCreateRoom.Decode(validPath, b); return err },
		"copyItems":   func(b []byte) error { _, err := RouteCopyMyPlaylistItems.Decode(validPath, b); return err },
	}
	tests := []struct {
		name string
		op   string
		body string
		want string
	}{
		{"修改资料：昵称 80 个字符", "updateMe", `{"name":"` + strings.Repeat("名", 80) + `"}`, ""},
		{"修改资料：昵称超过 80 个字符", "updateMe", `{"name":"` + strings.Repeat("名", 81) + `"}`, "name：长度不能超过 80 个字符"},
		{"修改资料：邮箱超过 320 个字符", "updateMe", `{"email":"` + strings.Repeat("a", 321) + `"}`, "email：长度不能超过 320 个字符"},
		{"建房：缺少描述", "createRoom", `{"name":"房间"}`, "description：缺少必填字段"},
		{"建房：描述超过 500 个字符", "createRoom", `{"name":"房间","description":"` + strings.Repeat("述", 501) + `"}`, "description：长度不能超过 500 个字符"},
		{"建房：名称为 null", "createRoom", `{"name":null,"description":""}`, "name：应为字符串"},
		{"后台建房：名称 80 个字符、描述 500 个字符", "adminCreate", `{"name":"` + strings.Repeat("名", 80) + `","description":"` + strings.Repeat("述", 500) + `"}`, ""},
		{"后台建房：名称超过 80 个字符", "adminCreate", `{"name":"` + strings.Repeat("名", 81) + `","description":""}`, "name：长度不能超过 80 个字符"},
		{"后台建房：别名", "adminCreate", `{"name":"房间","description":"","slug":"lofi-24"}`, ""},
		{"后台建房：别名不能为空", "adminCreate", `{"name":"房间","description":"","slug":""}`, "slug：格式不正确"},
		{"后台建房：别名超过 32 个字符", "adminCreate", `{"name":"房间","description":"","slug":"` + strings.Repeat("a", 33) + `"}`, "slug：格式不正确"},
		{"复制曲目：来源歌单", "copyItems", `{"from":"` + someUUID + `","tracks":[{"source":"qq","id":"1"}]}`, ""},
		{"复制曲目：来源歌单不是 UUID", "copyItems", `{"from":"not-a-uuid","tracks":[{"source":"qq","id":"1"}]}`, "from：格式不正确"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errText(decode[tt.op]([]byte(tt.body))); got != tt.want {
				t.Errorf("错误 = %q，期望 %q", got, tt.want)
			}
		})
	}
}

// 返回的函数把每个路径参数都取为 value，再连同 body 交给 decode。
func withPath[Req any](decode func(func(string) string, []byte) (Req, error), body string) func(value string) error {
	return func(value string) error {
		_, err := decode(func(string) string { return value }, []byte(body))
		return err
	}
}

func TestEndpointPathParams(t *testing.T) {
	shareID := strings.Repeat("a", 22)
	getRoom := withPath(RouteGetRoom.Decode, "")
	createTicket := withPath(RouteCreateTicket.Decode, "{}")
	deleteRoom := withPath(RouteAdminDeleteRoom.Decode, "")
	resetKey := withPath(RouteAdminResetRoomApiKey.Decode, "{}")
	items := withPath(RouteListMyPlaylistItems.Decode, "")
	shared := withPath(RouteGetSharedPlaylist.Decode, "")
	copyShared := withPath(RouteCopySharedPlaylist.Decode, `{"name":"抄来的"}`)
	account := withPath(RouteAdminGetAccount.Decode, "")
	tests := []struct {
		name   string
		decode func(string) error
		value  string
		// 为空时期望通过校验。
		wantType string
	}{
		{"房间：小写的 UUID", getRoom, someUUID, ""},
		{"房间：大写的 UUID", getRoom, strings.ToUpper(someUUID), ""},
		{"房间：别名", getRoom, "lofi-24", ""},
		{"房间：32 个字符的别名", getRoom, strings.Repeat("a", 32), ""},
		{"房间：别名超过 32 个字符", getRoom, strings.Repeat("a", 33), "RoomRef"},
		{"房间：别名有大写字母", getRoom, "HALL", "RoomRef"},
		{"房间：U+0000", getRoom, "\x00", "RoomRef"},
		{"房间：别名后面有 U+0000", getRoom, "hall\x00", "RoomRef"},
		{"房间：空", getRoom, "", "RoomRef"},
		{"换票据：UUID", createTicket, someUUID, ""},
		{"换票据：别名", createTicket, "hall", "RoomID"},
		{"换票据：UUID 后面有换行", createTicket, someUUID + "\n", "RoomID"},
		{"删除房间：U+0000", deleteRoom, "\x00", "RoomID"},
		{"重置 API Key：别名", resetKey, "hall", "RoomID"},
		{"歌单：UUID", items, someUUID, ""},
		{"歌单：不是 UUID", items, "not-a-uuid", "MyPlaylistID"},
		{"分享：22 个 base64url 字符", shared, "AZaz09-_" + shareID[8:], ""},
		{"分享：空", shared, "", "ShareID"},
		{"分享：太短", shared, "abc", "ShareID"},
		{"分享：21 个字符", shared, shareID[1:], "ShareID"},
		{"分享：23 个字符", shared, shareID + "a", "ShareID"},
		{"分享：U+0000", shared, shareID[1:] + "\x00", "ShareID"},
		{"分享：base64 的填充", shared, shareID[1:] + "=", "ShareID"},
		{"分享：标准 base64 的字符", shared, shareID[1:] + "+", "ShareID"},
		{"分享：斜杠", shared, shareID[1:] + "/", "ShareID"},
		{"分享：不是 UTF-8", shared, "\xff" + shareID[1:], "ShareID"},
		{"复制分享：不是分享 ID", copyShared, "not-a-share", "ShareID"},
		{"账号：用户名", account, "alice", ""},
		{"账号：有空格和大写字母", account, "Not Valid", "Username"},
		{"账号：太短", account, "ab", "Username"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.decode(tt.value)
			var param *PathParamError
			switch {
			case tt.wantType == "" && err != nil:
				t.Errorf("%q 的错误 = %v，期望通过", tt.value, err)
			case tt.wantType != "" && (!errors.As(err, &param) || param.Type != tt.wantType):
				t.Errorf("%q 的错误 = %v，期望 %s 类型的 PathParamError", tt.value, err, tt.wantType)
			}
		})
	}
}

func TestEndpointDecodesBodyBeforePathParams(t *testing.T) {
	_, err := RouteRenameMyPlaylist.Decode(func(string) string { return "not-a-uuid" }, []byte("{}"))
	if got := errText(err); got != "name：缺少必填字段" {
		t.Errorf("请求体和路径参数都不对时的错误 = %q，期望请求体的错误", got)
	}
	_, err = RouteRenameMyPlaylist.Decode(func(string) string { return "not-a-uuid" }, []byte(`{"name":"新名字"}`))
	var param *PathParamError
	if !errors.As(err, &param) || *param != (PathParamError{Name: "id", Type: "MyPlaylistID"}) {
		t.Errorf("只有路径参数不对时的错误 = %#v，期望 id 的 PathParamError", err)
	}
}

func TestErrorCodeHTTPStatus(t *testing.T) {
	tests := map[ErrorCode]int{
		ErrorCodeQueueFull: 409,
		ErrorCodeInternal:  500,
	}
	for code, want := range tests {
		if got := code.HTTPStatus(); got != want {
			t.Errorf("%s 的 HTTP 状态码 = %d，期望 %d", code, got, want)
		}
	}
}

func roomRoute(t *testing.T, pattern string) RoomRoute {
	t.Helper()
	for _, r := range RoomRoutes {
		if r.Pattern == pattern {
			return r
		}
	}
	t.Fatalf("没有房间内请求的路由 %s", pattern)
	return RoomRoute{}
}

func TestRoomRouteDecode(t *testing.T) {
	const (
		search   = "GET /api/room/search/tracks"
		playlist = "GET /api/room/playlists/{source}/{playlistId}/tracks"
		like     = "POST /api/room/queue/{entryId}/likes"
		vote     = "POST /api/room/skip-votes"
		settings = "PATCH /api/room/settings"
	)
	qq, keyword := SourceQQ, "晴天"
	tests := []struct {
		name    string
		pattern string
		path    map[string]string
		query   string
		body    string
		want    any
		wantErr string
	}{
		{"搜索：查询串按字段的类型解析", search, nil, "source=netease&keyword=%E6%99%B4%E5%A4%A9&page=2&pageSize=10", "", SearchTracksRequest{Source: SourceNetease, Keyword: "晴天", Page: 2, PageSize: 10}, ""},
		{"搜索：页码不是整数", search, nil, "source=netease&keyword=a&page=one&pageSize=10", "", nil, "page：应为整数"},
		{"搜索：页码超出范围", search, nil, "source=netease&keyword=a&page=0&pageSize=10", "", nil, "page：不能小于 1"},
		{"搜索：来源不是枚举中的值", search, nil, "source=apple&keyword=a&page=1&pageSize=10", "", nil, "source：取值应为 netease、qq、bilibili、link 之一"},
		{"搜索：缺少必填的参数", search, nil, "source=netease&keyword=a&page=1", "", nil, "pageSize：缺少必填字段"},
		{"搜索：重复的参数", search, nil, "source=netease&keyword=a&page=1&page=2&pageSize=10", "", nil, "page：参数重复"},
		{"搜索：未知参数", search, nil, "source=netease&keyword=a&page=1&pageSize=10&sort=hot", "", nil, "sort：未知参数"},
		{"搜索：查询串格式不正确", search, nil, "keyword=%zz", "", nil, "查询串格式不正确"},
		{"歌单：路径参数", playlist, map[string]string{"source": "netease", "playlistId": "3778678"}, "", "", PlaylistTracksRequest{Source: SourceNetease, PlaylistID: "3778678"}, ""},
		{"歌单：路径中的来源不是枚举中的值", playlist, map[string]string{"source": "apple", "playlistId": "1"}, "", "", nil, "source：取值应为 netease、qq、bilibili、link 之一"},
		{"删歌：DELETE 的路径参数", "DELETE /api/room/queue/{entryId}", map[string]string{"entryId": "7"}, "", "", QueueRemoveRequest{EntryID: "7"}, ""},
		{"点赞：不带请求体", like, map[string]string{"entryId": "7"}, "", "", QueueLikeRequest{EntryID: "7"}, ""},
		{"点赞：请求体为空对象", like, map[string]string{"entryId": "7"}, "", "{}", QueueLikeRequest{EntryID: "7"}, ""},
		{"点赞：请求体中有路径参数", like, map[string]string{"entryId": "7"}, "", `{"entryId":"8"}`, nil, "entryId：未知字段"},
		{"点赞：请求体为 null", like, map[string]string{"entryId": "7"}, "", "null", nil, "应为 JSON 对象"},
		{"投票：不带请求体", vote, nil, "", "", PlaybackVoteSkipRequest{}, ""},
		{"投票：请求体不是对象", vote, nil, "", "[]", nil, "应为 JSON 对象"},
		{"投票：请求体为 null", vote, nil, "", "null", nil, "应为 JSON 对象"},
		{"播放模式", settings, nil, "", `{"playMode":"rotation"}`, SettingsSetPlayModeRequest{PlayMode: PlayModeRotation}, ""},
		{"播放模式：缺少字段", settings, nil, "", "", nil, "playMode：缺少必填字段"},
		{"聊天：POST 不接受查询参数", "POST /api/room/messages", nil, "content=hi", `{"content":"你好"}`, nil, "content：未知参数"},
		{"点歌：请求体与 WebSocket 的 data 相同", "POST /api/room/queue", nil, "", `{"source":"qq","keyword":"晴天"}`, QueueAddRequest{Source: &qq, Keyword: &keyword}, ""},
		{"快照：GET 不读请求体", "GET /api/room", nil, "", "不是 JSON", RoomSyncRequest{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := roomRoute(t, tt.pattern).Decode(func(name string) string { return tt.path[name] }, tt.query, []byte(tt.body))
			if errText(err) != tt.wantErr || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Decode = %#v、%q，期望 %#v、%q", got, errText(err), tt.want, tt.wantErr)
			}
		})
	}
}
