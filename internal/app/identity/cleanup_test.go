package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

func newGuest(t *testing.T, store *porttest.Store, token string) member.Member {
	t.Helper()
	m, err := store.CreateMember(t.Context(), hashSecret(token), "游客-1a2b")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type logEntry struct {
	Level    string
	Msg      string
	Sessions int64
	Members  int64
	JobID    int64 `json:"job_id"`
	Error    string
}

func onlyEntry(t *testing.T, logs *bytes.Buffer) logEntry {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("应只有一条日志：\n%s", logs.String())
	}
	var entry logEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("日志不是 JSON：%v\n%s", err, lines[0])
	}
	return entry
}

func TestGuestCleanupThresholds(t *testing.T) {
	store := porttest.New()
	now := time.Now()
	used := map[string]time.Duration{"idle-under-7d": 7*24*time.Hour - time.Minute, "idle-over-7d": 7*24*time.Hour + time.Minute}
	members := map[string]member.Member{}
	for token, ago := range used {
		members[token] = newGuest(t, store, token)
		store.BackdateMember(t, members[token].ID, now.Add(-30*24*time.Hour))
		store.BackdateSession(t, hashSecret(token), now.Add(-ago))
	}
	created := map[string]time.Duration{"created-under-1d": 24*time.Hour - time.Minute, "created-over-1d": 24*time.Hour + time.Minute}
	for token, ago := range created {
		members[token] = newGuest(t, store, token)
		if err := store.DeleteSession(t.Context(), hashSecret(token)); err != nil {
			t.Fatal(err)
		}
		store.BackdateMember(t, members[token].ID, now.Add(-ago))
	}
	var logs bytes.Buffer
	cleanup := NewGuestCleanup(store, slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil))))

	if err := cleanup.Run(logctx.WithJob(t.Context(), 7), now); err != nil {
		t.Fatal(err)
	}

	for token, kept := range map[string]bool{"idle-under-7d": true, "idle-over-7d": false} {
		if _, _, err := store.MemberByToken(t.Context(), hashSecret(token)); (err == nil) != kept {
			t.Errorf("会话 %s 在清理之后的查找结果 = %v，期望保留 = %v", token, err, kept)
		}
	}
	for token, kept := range map[string]bool{"idle-under-7d": true, "idle-over-7d": false, "created-under-1d": true, "created-over-1d": false} {
		if _, err := store.AccountProfile(t.Context(), members[token].ID); (err == nil) != kept {
			t.Errorf("成员 %s 在清理之后的查找结果 = %v，期望保留 = %v", token, err, kept)
		}
	}
	if entry := onlyEntry(t, &logs); entry != (logEntry{Level: "INFO", Msg: "已清理闲置的游客", Sessions: 1, Members: 2, JobID: 7}) {
		t.Errorf("日志 = %+v，期望一条 info，带删除的 1 个会话、2 个成员和 ctx 中的 job_id", entry)
	}
}

type brokenGuests struct{}

func (brokenGuests) DeleteIdleGuests(context.Context, time.Time, time.Time) (int64, int64, error) {
	return 0, 0, errors.New("连接被重置")
}

// River 只在 info 级别记录出错的任务，internal/adapter/in/worker 又只输出 warn 及以上的 River 日志，所以清理失败要由用例自己记下来。
func TestGuestCleanupLogsFailure(t *testing.T) {
	var logs bytes.Buffer
	cleanup := NewGuestCleanup(brokenGuests{}, slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil))))
	if err := cleanup.Run(logctx.WithJob(t.Context(), 7), time.Now()); err == nil {
		t.Fatal("存储出错时 Run 应返回错误，交给 River 重试")
	}
	if entry := onlyEntry(t, &logs); entry != (logEntry{Level: "WARN", Msg: "清理闲置的游客失败", JobID: 7, Error: "连接被重置"}) {
		t.Errorf("日志 = %+v，期望一条 warn，带错误和 ctx 中的 job_id", entry)
	}
}
