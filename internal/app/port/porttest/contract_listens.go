package porttest

import (
	"errors"
	"fmt"
	"slices"
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

func record(t *testing.T, s ContractStore, members []string, l port.Listen) {
	t.Helper()
	if err := s.RecordListen(t.Context(), members, l); err != nil {
		t.Fatal(err)
	}
}

// 第 i 首是 s<i>，在 heard 之后 i 分钟听到，所以 s0 最早。
func recordSongs(t *testing.T, s ContractStore, members []string, n int) {
	t.Helper()
	for i := range n {
		record(t, s, members, songAt(fmt.Sprintf("s%d", i), heard.Add(time.Duration(i)*time.Minute)))
	}
}

// 时间统一换成 UTC，便于直接比较。
func listensOf(t *testing.T, s ContractStore, memberID string) []port.Listen {
	t.Helper()
	listens, err := s.MemberListens(t.Context(), memberID)
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

func runListensContract(t *testing.T, open func(t *testing.T) ContractStore) {
	t.Run("同一首歌只留最近一次听到的", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice", "游客-1a2b")
		bob := newGuest(t, s, "bob", "游客-3c4d")
		record(t, s, []string{alice.ID, bob.ID}, sunnyAt(heard))
		record(t, s, []string{alice.ID}, clipAt(heard.Add(time.Minute)))
		if got, want := listensOf(t, s, alice.ID), []port.Listen{clipAt(heard.Add(time.Minute)), sunnyAt(heard)}; !slices.Equal(got, want) {
			t.Errorf("alice 的历史 = %#v，期望最近听到的在前 %#v", got, want)
		}
		if got, want := listensOf(t, s, bob.ID), []port.Listen{sunnyAt(heard)}; !slices.Equal(got, want) {
			t.Errorf("bob 的历史 = %#v，期望 %#v", got, want)
		}

		// 同一首歌再次记录时，HeardAt 取较晚的，元数据用新的。
		renamed := sunnyAt(heard.Add(-time.Hour))
		renamed.Meta.Title = "晴天（重制）"
		record(t, s, []string{alice.ID}, renamed)
		want := sunnyAt(heard)
		want.Meta.Title = "晴天（重制）"
		if got := listensOf(t, s, alice.ID); !slices.Equal(got, []port.Listen{clipAt(heard.Add(time.Minute)), want}) {
			t.Errorf("较早的一次记录之后 = %#v，期望保留较晚的时间、换成新的元数据", got)
		}
		record(t, s, []string{alice.ID}, sunnyAt(heard.Add(time.Hour)))
		if got := heardIDs(listensOf(t, s, alice.ID)); !slices.Equal(got, []string{"186016", "https://example.com/a.mp3"}) {
			t.Errorf("再次听到《晴天》之后的顺序 = %v，期望它排到最前", got)
		}
	})

	t.Run("只记给开启了记录的成员", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice", "游客-1a2b")
		bob := newGuest(t, s, "bob", "游客-3c4d")
		if on, err := s.ListenRecording(t.Context(), bob.ID); err != nil || !on {
			t.Fatalf("新成员的 ListenRecording = %v, %v，期望默认开启", on, err)
		}
		record(t, s, []string{bob.ID}, sunnyAt(heard))
		if err := s.SetListenRecording(t.Context(), bob.ID, false); err != nil {
			t.Fatal(err)
		}
		if on, err := s.ListenRecording(t.Context(), bob.ID); err != nil || on {
			t.Errorf("关闭之后 ListenRecording = %v, %v", on, err)
		}
		record(t, s, []string{alice.ID, bob.ID, missingID}, clipAt(heard.Add(time.Minute)))
		if got := heardIDs(listensOf(t, s, alice.ID)); !slices.Equal(got, []string{"https://example.com/a.mp3"}) {
			t.Errorf("alice 的历史 = %v", got)
		}
		if got := heardIDs(listensOf(t, s, bob.ID)); !slices.Equal(got, []string{"186016"}) {
			t.Errorf("bob 关闭记录之后的历史 = %v，期望只有关闭之前的", got)
		}
		if err := s.SetListenRecording(t.Context(), bob.ID, true); err != nil {
			t.Fatal(err)
		}
		record(t, s, []string{bob.ID}, clipAt(heard.Add(2*time.Minute)))
		if got := heardIDs(listensOf(t, s, bob.ID)); len(got) != 2 {
			t.Errorf("重新开启之后的历史 = %v，期望继续记录", got)
		}

		if _, err := s.ListenRecording(t.Context(), missingID); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("不存在的成员的 ListenRecording 错误 = %v，期望 ErrMemberNotFound", err)
		}
		if err := s.SetListenRecording(t.Context(), missingID, false); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("不存在的成员的 SetListenRecording 错误 = %v，期望 ErrMemberNotFound", err)
		}
	})

	t.Run(fmt.Sprintf("每个成员只留最近听到的 %d 首", port.ListenLimit), func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice", "游客-1a2b")
		bob := newGuest(t, s, "bob", "游客-3c4d")
		recordSongs(t, s, []string{alice.ID, bob.ID}, port.ListenLimit)
		record(t, s, []string{alice.ID, bob.ID}, songAt("new", heard.Add(time.Hour*24)))
		for _, m := range []string{alice.ID, bob.ID} {
			got := heardIDs(listensOf(t, s, m))
			if len(got) != port.ListenLimit || got[0] != "new" || got[len(got)-1] != "s1" {
				t.Errorf("再记一首之后有 %d 首，最新 %q、最早 %q，期望 %d 首，s0 被删掉", len(got), got[0], got[len(got)-1], port.ListenLimit)
			}
		}
		// 比现有的都早的一首，记下之后它自己就是超出的部分。
		record(t, s, []string{alice.ID}, songAt("old", heard.Add(-time.Hour)))
		if got := heardIDs(listensOf(t, s, alice.ID)); slices.Contains(got, "old") || len(got) != port.ListenLimit {
			t.Errorf("更早的一首记下之后有 %d 首，含它 = %v，期望不留", len(got), slices.Contains(got, "old"))
		}
	})

	t.Run("删除和清空只影响本人的历史", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice", "游客-1a2b")
		bob := newGuest(t, s, "bob", "游客-3c4d")
		record(t, s, []string{alice.ID, bob.ID}, sunnyAt(heard))
		record(t, s, []string{alice.ID, bob.ID}, clipAt(heard.Add(time.Minute)))

		if err := s.DeleteListen(t.Context(), alice.ID, sunnyAt(heard).Track); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteListen(t.Context(), alice.ID, track.Track{Source: track.QQ, ID: "missing"}); err != nil {
			t.Errorf("删除不在历史里的歌 = %v，期望忽略", err)
		}
		if got := heardIDs(listensOf(t, s, alice.ID)); !slices.Equal(got, []string{"https://example.com/a.mp3"}) {
			t.Errorf("删除《晴天》之后 alice 的历史 = %v", got)
		}
		if err := s.ClearListens(t.Context(), alice.ID); err != nil {
			t.Fatal(err)
		}
		if got := listensOf(t, s, alice.ID); len(got) != 0 {
			t.Errorf("清空之后 alice 的历史 = %v", got)
		}
		if got := heardIDs(listensOf(t, s, bob.ID)); len(got) != 2 {
			t.Errorf("alice 删除和清空之后 bob 的历史 = %v，期望不受影响", got)
		}
	})

	t.Run("登录时把游客的历史并入账号", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		guest := newGuest(t, s, "guest", "游客-3c4d")
		sunny := sunnyAt(heard.Add(time.Hour))
		sunny.Meta.Title = "晴天（游客听到的）"
		record(t, s, []string{alice.ID}, sunnyAt(heard))
		record(t, s, []string{alice.ID}, songAt("1", heard.Add(time.Minute)))
		record(t, s, []string{guest.ID}, sunny)
		record(t, s, []string{guest.ID}, clipAt(heard.Add(-time.Hour)))

		if err := s.ReplaceSession(t.Context(), sessionHash("guest"), alice.ID, sessionHash("alice-2"), "hash"); err != nil {
			t.Fatal(err)
		}
		if got, want := listensOf(t, s, alice.ID), []port.Listen{sunny, songAt("1", heard.Add(time.Minute)), clipAt(heard.Add(-time.Hour))}; !slices.Equal(got, want) {
			t.Errorf("登录之后账号的历史 = %#v，期望并入游客的历史 %#v", got, want)
		}
		if got := listensOf(t, s, guest.ID); len(got) != 0 {
			t.Errorf("登录之后游客的历史 = %v，期望移走", got)
		}
	})

	t.Run("并入时元数据用游客那份，时间取较晚的", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		guest := newGuest(t, s, "guest", "游客-3c4d")
		earlier := sunnyAt(heard)
		earlier.Meta.Title = "晴天（游客听到的）"
		record(t, s, []string{alice.ID}, sunnyAt(heard.Add(time.Hour)))
		record(t, s, []string{guest.ID}, earlier)

		if err := s.ReplaceSession(t.Context(), sessionHash("guest"), alice.ID, sessionHash("alice-2"), "hash"); err != nil {
			t.Fatal(err)
		}
		want := earlier
		want.HeardAt = heard.Add(time.Hour)
		if got := listensOf(t, s, alice.ID); !slices.Equal(got, []port.Listen{want}) {
			t.Errorf("登录之后账号的历史 = %#v，期望 %#v", got, []port.Listen{want})
		}
	})

	t.Run("并入游客的历史遵守记录的规则", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		guest := newGuest(t, s, "guest", "游客-3c4d")
		other := newGuest(t, s, "other", "游客-5e6f")
		recordSongs(t, s, []string{alice.ID}, port.ListenLimit)
		record(t, s, []string{guest.ID}, songAt("new", heard.Add(24*time.Hour)))
		if err := s.ReplaceSession(t.Context(), sessionHash("guest"), alice.ID, sessionHash("alice-2"), "hash"); err != nil {
			t.Fatal(err)
		}
		if got := heardIDs(listensOf(t, s, alice.ID)); len(got) != port.ListenLimit || got[0] != "new" || slices.Contains(got, "s0") {
			t.Errorf("并入之后账号有 %d 首，期望 %d 首、最早的 s0 被删掉", len(got), port.ListenLimit)
		}

		if err := s.SetListenRecording(t.Context(), alice.ID, false); err != nil {
			t.Fatal(err)
		}
		record(t, s, []string{other.ID}, songAt("other", heard.Add(48*time.Hour)))
		if err := s.ReplaceSession(t.Context(), sessionHash("other"), alice.ID, sessionHash("alice-3"), "hash"); err != nil {
			t.Fatal(err)
		}
		if got := heardIDs(listensOf(t, s, alice.ID)); slices.Contains(got, "other") {
			t.Error("账号关闭记录时不应并入游客的历史")
		}
		if got := listensOf(t, s, other.ID); len(got) != 0 {
			t.Errorf("游客的历史 = %v，账号不并入时也随会话一起删掉", got)
		}
	})

	t.Run("登录被拒绝时历史不变", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		guest := newGuest(t, s, "guest", "游客-3c4d")
		record(t, s, []string{guest.ID}, sunnyAt(heard))
		if err := s.ReplaceSession(t.Context(), sessionHash("guest"), alice.ID, sessionHash("alice-2"), "changed"); !errors.Is(err, port.ErrAccountPasswordChanged) {
			t.Fatalf("密码哈希不符时 = %v", err)
		}
		if err := s.ReplaceSession(t.Context(), sessionHash("nobody"), alice.ID, sessionHash("alice-2"), "hash"); !errors.Is(err, port.ErrSessionNotFound) {
			t.Fatalf("旧会话不存在时 = %v", err)
		}
		if got := listensOf(t, s, guest.ID); len(got) != 1 {
			t.Errorf("登录被拒绝之后游客的历史 = %v，期望不变", got)
		}
		if got := listensOf(t, s, alice.ID); len(got) != 0 {
			t.Errorf("登录被拒绝之后账号的历史 = %v，期望不变", got)
		}
	})
}
