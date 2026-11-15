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

// 同时开播的几个房间里有同样的两个成员，各房间给出成员的顺序不同。并发写入都成功，每首歌只留一条。
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

func TestListensAreDeletedWithMember(t *testing.T) {
	store, pool := newStore(t)
	bob := createMember(t, store, "bob", "游客-3c4d")
	record(t, store, []string{bob.ID}, sunnyAt(heard))

	if _, err := pool.Exec(t.Context(), "DELETE FROM members WHERE id = $1", bob.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM listens WHERE member_id = $1", bob.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("删除成员之后还剩 %d 条历史（%v），期望随成员删除", left, err)
	}
}

// 两台设备上的游客同时登录同一个账号，两边听过同一批歌，写入的先后相反。两次合并依次进行，都成功，账号里每首歌只留一条。
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
