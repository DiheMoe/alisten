package postgres

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var heard = time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)

func sunnyAt(at time.Time) port.Listen {
	return port.Listen{
		Track:   track.Track{Source: track.NetEase, ID: "186016"},
		Meta:    track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/a.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269500 * time.Millisecond},
		HeardAt: at,
	}
}

func clipAt(at time.Time) port.Listen {
	return port.Listen{
		Track:   track.Track{Source: track.Link, ID: "https://example.com/a.mp3"},
		Meta:    track.Meta{Title: "a.mp3", CoverKey: "covers/abc", PageURL: "https://example.com/a.mp3", Duration: time.Minute},
		HeardAt: at,
	}
}

func songAt(id string, at time.Time) port.Listen {
	return port.Listen{Track: track.Track{Source: track.NetEase, ID: id}, Meta: track.Meta{Title: id, PageURL: track.NetEasePageURL(id)}, HeardAt: at}
}

func record(t *testing.T, store *Store, members []string, l port.Listen) {
	t.Helper()
	if err := store.RecordListen(t.Context(), members, l); err != nil {
		t.Fatal(err)
	}
}

// 时间统一换成 UTC，便于直接比较。
func listensOf(t *testing.T, store *Store, memberID string) []port.Listen {
	t.Helper()
	listens, err := store.MemberListens(t.Context(), memberID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range listens {
		listens[i].HeardAt = listens[i].HeardAt.UTC()
	}
	return listens
}

func heardIDs(listens []port.Listen) []string {
	ids := make([]string, len(listens))
	for i, l := range listens {
		ids[i] = l.Track.ID
	}
	return ids
}

func TestRecordListenKeepsLatestHearingOfEachTrack(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	bob := createMember(t, store, "bob", "游客-3c4d")
	record(t, store, []string{alice.ID, bob.ID}, sunnyAt(heard))
	record(t, store, []string{alice.ID}, clipAt(heard.Add(time.Minute)))
	if got, want := listensOf(t, store, alice.ID), []port.Listen{clipAt(heard.Add(time.Minute)), sunnyAt(heard)}; !slices.Equal(got, want) {
		t.Errorf("alice 的历史 = %#v，期望最近听到的在前 %#v", got, want)
	}
	if got, want := listensOf(t, store, bob.ID), []port.Listen{sunnyAt(heard)}; !slices.Equal(got, want) {
		t.Errorf("bob 的历史 = %#v，期望 %#v", got, want)
	}

	// 同一首歌再次记录：heard_at 取较晚的，元数据用新的。
	renamed := sunnyAt(heard.Add(-time.Hour))
	renamed.Meta.Title = "晴天（重制）"
	record(t, store, []string{alice.ID}, renamed)
	want := sunnyAt(heard)
	want.Meta.Title = "晴天（重制）"
	if got := listensOf(t, store, alice.ID); !slices.Equal(got, []port.Listen{clipAt(heard.Add(time.Minute)), want}) {
		t.Errorf("较早的一次记录之后 = %#v，期望保留较晚的时间、换成新的元数据", got)
	}
	record(t, store, []string{alice.ID}, sunnyAt(heard.Add(time.Hour)))
	if got := heardIDs(listensOf(t, store, alice.ID)); !slices.Equal(got, []string{"186016", "https://example.com/a.mp3"}) {
		t.Errorf("再次听到《晴天》之后的顺序 = %v，期望它排到最前", got)
	}
}

func TestRecordListenTruncatesToStoredPrecision(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	l := sunnyAt(heard.Add(1234567 * time.Nanosecond))
	l.Meta.Duration = 3*time.Minute + 1234*time.Microsecond
	record(t, store, []string{alice.ID}, l)
	got := listensOf(t, store, alice.ID)
	if len(got) != 1 || got[0].Meta.Duration != 3*time.Minute+time.Millisecond || !got[0].HeardAt.Equal(heard.Add(1234*time.Microsecond)) {
		t.Errorf("历史 = %#v，期望时长按毫秒、时间按微秒截断", got)
	}
}

func TestRecordListenSkipsMembersWhoTurnedRecordingOff(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	bob := createMember(t, store, "bob", "游客-3c4d")
	if on, err := store.ListenRecording(t.Context(), bob.ID); err != nil || !on {
		t.Fatalf("新成员的 ListenRecording = %v, %v，期望默认开启", on, err)
	}
	record(t, store, []string{bob.ID}, sunnyAt(heard))
	if err := store.SetListenRecording(t.Context(), bob.ID, false); err != nil {
		t.Fatal(err)
	}
	if on, err := store.ListenRecording(t.Context(), bob.ID); err != nil || on {
		t.Errorf("关闭之后 ListenRecording = %v, %v", on, err)
	}
	record(t, store, []string{alice.ID, bob.ID, "01a15778-1911-7a28-8b2e-e08c2a25f388"}, clipAt(heard.Add(time.Minute)))
	if got := heardIDs(listensOf(t, store, alice.ID)); !slices.Equal(got, []string{"https://example.com/a.mp3"}) {
		t.Errorf("alice 的历史 = %v", got)
	}
	if got := heardIDs(listensOf(t, store, bob.ID)); !slices.Equal(got, []string{"186016"}) {
		t.Errorf("bob 关闭记录之后的历史 = %v，期望只有关闭之前的", got)
	}
	if err := store.SetListenRecording(t.Context(), bob.ID, true); err != nil {
		t.Fatal(err)
	}
	record(t, store, []string{bob.ID}, clipAt(heard.Add(2*time.Minute)))
	if got := heardIDs(listensOf(t, store, bob.ID)); len(got) != 2 {
		t.Errorf("重新开启之后的历史 = %v，期望继续记录", got)
	}

	missing := "01a15778-1911-7a28-8b2e-e08c2a25f388"
	if _, err := store.ListenRecording(t.Context(), missing); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("不存在的成员的 ListenRecording 错误 = %v，期望 ErrMemberNotFound", err)
	}
	if err := store.SetListenRecording(t.Context(), missing, false); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("不存在的成员的 SetListenRecording 错误 = %v，期望 ErrMemberNotFound", err)
	}
}

func TestRecordListenKeepsLatest500(t *testing.T) {
	store, pool := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	bob := createMember(t, store, "bob", "游客-3c4d")
	// 第 i 首在 heard 之后 i 分钟听到，s0 最早。
	for _, m := range []string{alice.ID, bob.ID} {
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO listens (member_id, source, track_id, title, duration_ms, page_url, heard_at)
			SELECT $1, 'netease', 's' || i, 's' || i, 0, 'https://music.163.com', $2::timestamptz + i * interval '1 minute'
			FROM generate_series(0, 499) AS i`, m, heard); err != nil {
			t.Fatal(err)
		}
	}
	record(t, store, []string{alice.ID, bob.ID}, songAt("new", heard.Add(time.Hour*24)))
	for _, m := range []string{alice.ID, bob.ID} {
		got := heardIDs(listensOf(t, store, m))
		if len(got) != port.ListenLimit || got[0] != "new" || got[len(got)-1] != "s1" {
			t.Errorf("第 501 首之后有 %d 首，最新 %q、最早 %q，期望 500 首，s0 被删掉", len(got), got[0], got[len(got)-1])
		}
	}
	// 比现有的都早的一首，记下之后它自己就是超出的部分。
	record(t, store, []string{alice.ID}, songAt("old", heard.Add(-time.Hour)))
	if got := heardIDs(listensOf(t, store, alice.ID)); slices.Contains(got, "old") || len(got) != port.ListenLimit {
		t.Errorf("更早的一首记下之后有 %d 首，含它 = %v，期望不留", len(got), slices.Contains(got, "old"))
	}
}

// 同时开播的几个房间里有同样的两个成员，各自按不同的顺序给出成员，并发写入都成功，每首歌只留一条。
func TestConcurrentRecordsOfSameMembers(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	bob := createMember(t, store, "bob", "游客-3c4d")
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		members := []string{alice.ID, bob.ID}
		if i%2 == 1 {
			members = []string{bob.ID, alice.ID}
		}
		wg.Go(func() {
			errs <- store.RecordListen(t.Context(), members, songAt(fmt.Sprint(i/2%4), heard.Add(time.Duration(i)*time.Second)))
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []string{alice.ID, bob.ID} {
		if got := heardIDs(listensOf(t, store, m)); !slices.Equal(got, []string{"3", "2", "1", "0"}) {
			t.Errorf("并发记录之后的历史 = %v，期望每首一条、按最后一次听到的时间排列", got)
		}
	}
}

func TestDeleteAndClearListens(t *testing.T) {
	store, pool := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	bob := createMember(t, store, "bob", "游客-3c4d")
	record(t, store, []string{alice.ID, bob.ID}, sunnyAt(heard))
	record(t, store, []string{alice.ID, bob.ID}, clipAt(heard.Add(time.Minute)))

	if err := store.DeleteListen(t.Context(), alice.ID, sunnyAt(heard).Track); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteListen(t.Context(), alice.ID, track.Track{Source: track.QQ, ID: "missing"}); err != nil {
		t.Errorf("删除不在历史里的歌 = %v，期望忽略", err)
	}
	if got := heardIDs(listensOf(t, store, alice.ID)); !slices.Equal(got, []string{"https://example.com/a.mp3"}) {
		t.Errorf("删除《晴天》之后 alice 的历史 = %v", got)
	}
	if err := store.ClearListens(t.Context(), alice.ID); err != nil {
		t.Fatal(err)
	}
	if got := listensOf(t, store, alice.ID); len(got) != 0 {
		t.Errorf("清空之后 alice 的历史 = %v", got)
	}
	if got := heardIDs(listensOf(t, store, bob.ID)); len(got) != 2 {
		t.Errorf("alice 删除和清空之后 bob 的历史 = %v，期望不受影响", got)
	}

	if _, err := pool.Exec(t.Context(), "DELETE FROM members WHERE id = $1", bob.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM listens WHERE member_id = $1", bob.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("删除成员之后还剩 %d 条历史（%v），期望随成员删除", left, err)
	}
}

func TestReplaceSessionMovesGuestListensIntoAccount(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	guest := createMember(t, store, "guest", "游客-3c4d")
	sunny := sunnyAt(heard.Add(time.Hour))
	sunny.Meta.Title = "晴天（游客听到的）"
	record(t, store, []string{alice.ID}, sunnyAt(heard))
	record(t, store, []string{alice.ID}, songAt("1", heard.Add(time.Minute)))
	record(t, store, []string{guest.ID}, sunny)
	record(t, store, []string{guest.ID}, clipAt(heard.Add(-time.Hour)))

	if err := store.ReplaceSession(t.Context(), tokenHash("guest"), alice.ID, tokenHash("alice-2"), "hash"); err != nil {
		t.Fatal(err)
	}
	if got, want := listensOf(t, store, alice.ID), []port.Listen{sunny, songAt("1", heard.Add(time.Minute)), clipAt(heard.Add(-time.Hour))}; !slices.Equal(got, want) {
		t.Errorf("登录之后账号的历史 = %#v，期望并入游客的历史 %#v", got, want)
	}
	if got := listensOf(t, store, guest.ID); len(got) != 0 {
		t.Errorf("登录之后游客的历史 = %v，期望移走", got)
	}
}

func TestReplaceSessionMergeFollowsRecordingRules(t *testing.T) {
	store, pool := newStore(t)
	alice := registered(t, store, "alice")
	guest := createMember(t, store, "guest", "游客-3c4d")
	other := createMember(t, store, "other", "游客-5e6f")
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO listens (member_id, source, track_id, title, duration_ms, page_url, heard_at)
		SELECT $1, 'netease', 's' || i, 's' || i, 0, 'https://music.163.com', $2::timestamptz + i * interval '1 minute'
		FROM generate_series(0, 499) AS i`, alice.ID, heard); err != nil {
		t.Fatal(err)
	}
	record(t, store, []string{guest.ID}, songAt("new", heard.Add(24*time.Hour)))
	if err := store.ReplaceSession(t.Context(), tokenHash("guest"), alice.ID, tokenHash("alice-2"), "hash"); err != nil {
		t.Fatal(err)
	}
	if got := heardIDs(listensOf(t, store, alice.ID)); len(got) != port.ListenLimit || got[0] != "new" || slices.Contains(got, "s0") {
		t.Errorf("并入之后账号有 %d 首，期望 500 首、最早的 s0 被删掉", len(got))
	}

	if err := store.SetListenRecording(t.Context(), alice.ID, false); err != nil {
		t.Fatal(err)
	}
	record(t, store, []string{other.ID}, songAt("other", heard.Add(48*time.Hour)))
	if err := store.ReplaceSession(t.Context(), tokenHash("other"), alice.ID, tokenHash("alice-3"), "hash"); err != nil {
		t.Fatal(err)
	}
	if got := heardIDs(listensOf(t, store, alice.ID)); slices.Contains(got, "other") {
		t.Error("账号关闭记录时不应并入游客的历史")
	}
	if got := listensOf(t, store, other.ID); len(got) != 0 {
		t.Errorf("游客的历史 = %v，账号不并入时也随会话一起删掉", got)
	}
}

func TestReplaceSessionKeepsListensWhenRejected(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	guest := createMember(t, store, "guest", "游客-3c4d")
	record(t, store, []string{guest.ID}, sunnyAt(heard))
	if err := store.ReplaceSession(t.Context(), tokenHash("guest"), alice.ID, tokenHash("alice-2"), "changed"); !errors.Is(err, port.ErrAccountPasswordChanged) {
		t.Fatalf("密码哈希不符时 = %v", err)
	}
	if err := store.ReplaceSession(t.Context(), tokenHash("nobody"), alice.ID, tokenHash("alice-2"), "hash"); !errors.Is(err, port.ErrSessionNotFound) {
		t.Fatalf("旧会话不存在时 = %v", err)
	}
	if got := listensOf(t, store, guest.ID); len(got) != 1 {
		t.Errorf("登录被拒绝之后游客的历史 = %v，期望不变", got)
	}
	if got := listensOf(t, store, alice.ID); len(got) != 0 {
		t.Errorf("登录被拒绝之后账号的历史 = %v，期望不变", got)
	}
}

// 两台设备上的游客同时登录同一个账号，两边听过同一批歌、写入的先后相反：两次合并依次进行，都成功，账号里每首歌只留一条。
func TestConcurrentReplaceSessionsMergeIntoSameAccount(t *testing.T) {
	store, pool := newStore(t)
	const songs = 200
	for round := range 5 {
		alice := registered(t, store, fmt.Sprintf("alice-%d", round))
		guests := []string{fmt.Sprintf("guest-%d-a", round), fmt.Sprintf("guest-%d-b", round)}
		for i, token := range guests {
			guest := createMember(t, store, token, "游客-"+token)
			if _, err := pool.Exec(t.Context(), `
				INSERT INTO listens (member_id, source, track_id, title, duration_ms, page_url, heard_at)
				SELECT $1, 'netease', 's' || i, 's' || i, 0, 'https://music.163.com', $2::timestamptz + i * interval '1 minute'
				FROM generate_series(0, $3 - 1) AS i ORDER BY i * $4`, guest.ID, heard, songs, 1-2*i); err != nil {
				t.Fatal(err)
			}
		}

		start := make(chan struct{})
		errs := make([]error, len(guests))
		var wg sync.WaitGroup
		for i, token := range guests {
			wg.Go(func() {
				<-start
				errs[i] = store.ReplaceSession(t.Context(), tokenHash(token), alice.ID, tokenHash(token+"-alice"), "hash")
			})
		}
		close(start)
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatalf("第 %d 轮同时登录：%v", round, err)
		}
		got := heardIDs(listensOf(t, store, alice.ID))
		if len(got) != songs || got[0] != fmt.Sprintf("s%d", songs-1) || got[songs-1] != "s0" {
			t.Errorf("第 %d 轮并入之后账号有 %d 首，期望 %d 首、每首一条、按听到的时间排列", round, len(got), songs)
		}
	}
}
