package porttest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
)

func issueTicket(t *testing.T, s ContractStore, ticket, roomID, memberID, session string) {
	t.Helper()
	if err := s.CreateTicket(t.Context(), port.NewTicket{TokenHash: sessionHash(ticket), RoomID: roomID, MemberID: memberID, SessionTokenHash: sessionHash(session)}); err != nil {
		t.Fatal(err)
	}
}

func ticketValid(t *testing.T, s ContractStore, ticket string) bool {
	t.Helper()
	_, err := s.TicketHolder(t.Context(), sessionHash(ticket))
	if err != nil && !errors.Is(err, port.ErrTicketNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

func memberExists(t *testing.T, s ContractStore, memberID string) bool {
	t.Helper()
	_, err := s.ListenRecording(t.Context(), memberID)
	if err != nil && !errors.Is(err, port.ErrMemberNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

func loginAs(t *testing.T, s ContractStore, guestToken, accountID, accountToken, passwordHash string) {
	t.Helper()
	if err := s.ReplaceSession(t.Context(), sessionHash(guestToken), accountID, sessionHash(accountToken), passwordHash); err != nil {
		t.Fatal(err)
	}
}

func runGuestsContract(t *testing.T, open func(t *testing.T) ContractStore) {
	const day = 24 * time.Hour
	const week = 7 * day

	t.Run("清理闲置的游客会话和没有会话的游客", func(t *testing.T) {
		s := open(t)
		now := time.Now()
		hall := newRoom(t, s, port.NewRoom{Kind: room.Persistent, Name: "大厅", Capacity: 64})

		idle := newGuest(t, s, "idle", "游客-1a2b")
		s.BackdateMember(t, idle.ID, now.Add(-4*week))
		s.BackdateSession(t, sessionHash("idle"), now.Add(-week-time.Hour))
		issueTicket(t, s, "idle-ticket", hall, idle.ID, "idle")
		record(t, s, []string{idle.ID}, sunnyAt(heard))

		recent := newGuest(t, s, "recent", "游客-3c4d")
		s.BackdateMember(t, recent.ID, now.Add(-4*week))
		s.BackdateSession(t, sessionHash("recent"), now.Add(-week+time.Hour))

		alice := newGuest(t, s, "alice", "游客-5e6f")
		if err := s.Register(t.Context(), alice.ID, "alice", "hash-a"); err != nil {
			t.Fatal(err)
		}
		octocat := newGuest(t, s, "octocat", "游客-7a8b")
		if err := s.BindGitHub(t.Context(), octocat.ID, 42); err != nil {
			t.Fatal(err)
		}
		for _, account := range []struct{ id, token string }{{alice.ID, "alice"}, {octocat.ID, "octocat"}} {
			s.BackdateMember(t, account.id, now.Add(-4*week))
			s.BackdateSession(t, sessionHash(account.token), now.Add(-4*week))
		}

		// 登录账号时，游客的会话换给了账号，游客自己就没有会话了。
		left := newGuest(t, s, "left", "游客-9c0d")
		loginAs(t, s, "left", alice.ID, "alice-2", "hash-a")
		s.BackdateMember(t, left.ID, now.Add(-2*day))
		fresh := newGuest(t, s, "fresh", "游客-1e2f")
		loginAs(t, s, "fresh", alice.ID, "alice-3", "hash-a")

		// 票据挂在另一个成员的会话上，只是为了留下一张引用这个游客、但不随会话删除的票据。
		holder := newGuest(t, s, "holder", "游客-3a4b")
		loginAs(t, s, "holder", alice.ID, "alice-4", "hash-a")
		s.BackdateMember(t, holder.ID, now.Add(-2*day))
		issueTicket(t, s, "holder-ticket", hall, holder.ID, "recent")

		if err := s.ResetRoomAPIKey(t.Context(), hall, port.NewAPIKey{TokenHash: sessionHash("key")}); err != nil {
			t.Fatal(err)
		}
		bot, err := s.TicketHolder(t.Context(), sessionHash("key"))
		if err != nil {
			t.Fatal(err)
		}
		s.BackdateMember(t, bot.Member.ID, now.Add(-4*week))

		sessions, members, err := s.DeleteIdleGuests(t.Context(), now.Add(-week), now.Add(-day))
		if err != nil || sessions != 1 || members != 2 {
			t.Errorf("DeleteIdleGuests = %d, %d, %v，期望删除 idle 的会话，以及 idle 和 left 两个游客", sessions, members, err)
		}
		if sessionOwner(t, s, "idle") != "" || ticketValid(t, s, "idle-ticket") {
			t.Error("闲置超过 7 天的游客会话和它的票据应被删除")
		}
		for token, owner := range map[string]string{"recent": recent.ID, "alice": alice.ID, "alice-2": alice.ID, "octocat": octocat.ID} {
			if got := sessionOwner(t, s, token); got != owner {
				t.Errorf("会话 %s 属于 %q，期望保留，仍属于 %s", token, got, owner)
			}
		}
		if !ticketValid(t, s, "holder-ticket") || !ticketValid(t, s, "key") {
			t.Error("没有被删除的会话的票据和 API Key 应仍然有效")
		}
		for _, m := range []struct {
			name, id string
			kept     bool
		}{
			{"会话闲置的游客", idle.ID, false},
			{"登录了账号的游客", left.ID, false},
			{"还有会话的游客", recent.ID, true},
			{"创建不到 1 天的游客", fresh.ID, true},
			{"仍被票据引用的游客", holder.ID, true},
			{"bot 成员", bot.Member.ID, true},
			{"设置了用户名的账号", alice.ID, true},
			{"只绑定了 GitHub 的账号", octocat.ID, true},
		} {
			if got := memberExists(t, s, m.id); got != m.kept {
				t.Errorf("%s在清理之后存在 = %v，期望 %v", m.name, got, m.kept)
			}
		}
	})

	t.Run("会话被用到时记录使用时间，一天之内只记一次", func(t *testing.T) {
		s := open(t)
		lobby := newRoom(t, s, port.NewRoom{Kind: room.Ephemeral, Name: "临时", Capacity: 16})
		uses := []struct {
			name string
			use  func(token string) error
		}{
			{"成员 token 鉴权", func(token string) error {
				_, _, err := s.MemberByToken(t.Context(), sessionHash(token))
				return err
			}},
			{"用票据进房", func(token string) error {
				_, err := s.TicketHolder(t.Context(), sessionHash(token+"-ticket"))
				return err
			}},
			{"核对连接的凭证", func(token string) error {
				_, err := s.ExistingTickets(t.Context(), [][]byte{sessionHash(token + "-ticket")})
				return err
			}},
		}
		now := time.Now()
		for i, u := range uses {
			for _, c := range []struct {
				suffix string
				used   time.Time
			}{{"stale", now.Add(-day - time.Hour)}, {"recent", now.Add(-day + time.Hour)}} {
				token := fmt.Sprintf("%d-%s", i, c.suffix)
				m := newGuest(t, s, token, "游客-1a2b")
				issueTicket(t, s, token+"-ticket", lobby, m.ID, token)
				s.BackdateSession(t, sessionHash(token), c.used)
				if err := u.use(token); err != nil {
					t.Fatalf("%s：%v", u.name, err)
				}
			}
		}

		// 只删 1 小时之内没有记录过使用时间的会话。成员都是刚创建的，所以一个也不删。
		sessions, members, err := s.DeleteIdleGuests(t.Context(), time.Now().Add(-time.Hour), now.Add(-day))
		if err != nil || sessions != int64(len(uses)) || members != 0 {
			t.Errorf("DeleteIdleGuests = %d, %d, %v，期望删除 %d 个 24 小时之内记录过的会话", sessions, members, err, len(uses))
		}
		for i, u := range uses {
			if sessionOwner(t, s, fmt.Sprintf("%d-stale", i)) == "" {
				t.Errorf("%s之后，超过 24 小时没有记录的会话应更新使用时间", u.name)
			}
			if sessionOwner(t, s, fmt.Sprintf("%d-recent", i)) != "" {
				t.Errorf("%s之后，24 小时之内记录过的会话不应再更新使用时间", u.name)
			}
		}
	})
}
