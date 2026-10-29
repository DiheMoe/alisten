package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestHTTPRequestValidation(t *testing.T) {
	decode := map[string]func([]byte) error{
		"updateMe":    func(b []byte) error { _, err := DecodeUpdateMeRequest(b); return err },
		"createRoom":  func(b []byte) error { _, err := DecodeCreateRoomRequest(b); return err },
		"adminCreate": func(b []byte) error { _, err := DecodeAdminCreateRoomRequest(b); return err },
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errText(decode[tt.op]([]byte(tt.body))); got != tt.want {
				t.Errorf("错误 = %q，期望 %q", got, tt.want)
			}
		})
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
