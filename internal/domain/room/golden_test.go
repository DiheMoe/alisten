package room

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/txtar"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var update = flag.Bool("update", false, "用实际输出重写 testdata 中的期望")

var goldenMembers = map[string]member.Member{"m1": alice, "m2": bob, "m3": carol}

func TestGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txtar")
	if err != nil || len(files) == 0 {
		t.Fatalf("没有找到用例：%v", err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			archive, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var r *Room
			changed := false
			for i := range archive.Files {
				f := &archive.Files[i]
				input, want, _ := strings.Cut(string(f.Data), "\n")
				var effects []Effect
				r, effects, err = runStep(r, f.Name, strings.TrimPrefix(input, "> "))
				if err != nil {
					t.Fatalf("步骤 %s：%v", f.Name, err)
				}
				got := formatEffects(effects)
				if got == want {
					continue
				}
				if *update {
					f.Data = []byte(input + "\n" + got)
					changed = true
					continue
				}
				t.Errorf("步骤 %s：%s\n实际：\n%s期望：\n%s", f.Name, input, got, want)
			}
			if changed {
				if err := os.WriteFile(file, txtar.Format(archive), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

var roomKinds = map[string]Kind{"ephemeral": Ephemeral, "persistent": Persistent}

func runStep(r *Room, step, line string) (*Room, []Effect, error) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return r, nil, errors.New("缺少输入")
	}
	if f[0] == "restore" {
		if r != nil {
			return r, nil, errors.New("restore 只能是第一步")
		}
		if len(f) != 2 {
			return nil, nil, fmt.Errorf("restore 之后应是房间类型：%q", line)
		}
		kind, err := lookup(roomKinds, "房间类型", f[1])
		if err != nil {
			return nil, nil, err
		}
		r, effects := Restore(at(0), 1, Snapshot{Info: Info{ID: "r1", Name: "房间", Kind: kind}, Mode: Sequential})
		return r, effects, nil
	}
	if r == nil {
		return nil, nil, errors.New("第一步必须是 restore")
	}
	n, err := strconv.Atoi(step)
	if err != nil {
		return r, nil, fmt.Errorf("步骤名必须是数字：%q", step)
	}
	offset, err := time.ParseDuration(f[0])
	if err != nil {
		return r, nil, fmt.Errorf("时间不合法：%w", err)
	}
	in, err := parseInput(RequestID("q"+step), f[1:])
	if err != nil {
		return r, nil, err
	}
	return r, r.Handle(Env{Now: t0.Add(offset), Seed: uint64(n)}, in), nil
}

var inputArity = map[string]int{
	"join": 2, "leave": 1, "enqueue": 2, "remove": 2, "like": 2, "vote": 1, "mode": 2,
	"chat": 1, "sync": 1, "prepared": 2, "failed": 1, "recommended": 2, "timer": 1,
}

var playModes = map[string]PlayMode{"sequential": Sequential, "random": Random, "rotation": Rotation}

func parseInput(id RequestID, f []string) (Input, error) {
	if len(f) == 0 {
		return nil, errors.New("缺少输入")
	}
	verb, args := f[0], f[1:]
	n, ok := inputArity[verb]
	switch {
	case !ok:
		return nil, fmt.Errorf("未知的输入：%q", verb)
	case len(args) < n || len(args) > n && verb != "chat" && verb != "failed":
		return nil, fmt.Errorf("%s 应有 %d 个参数：%q", verb, n, args)
	}
	cmd := func(a Action) (Input, error) {
		m, err := lookup(goldenMembers, "成员", args[0])
		return Command{RequestID: id, Member: m, Action: a}, err
	}
	switch verb {
	case "join":
		m, err := lookup(goldenMembers, "成员", args[1])
		return Join{Session: SessionID(args[0]), Member: m}, err
	case "leave":
		return Leave{Session: SessionID(args[0])}, nil
	case "enqueue":
		tr, err := parseTrack(args[1])
		if err != nil {
			return nil, err
		}
		return cmd(Enqueue{Track: tr, Meta: track.Meta{Title: tr.ID}})
	case "remove", "like":
		e, err := parseNumber(args[1])
		if err != nil {
			return nil, err
		}
		if verb == "remove" {
			return cmd(Remove{Entry: EntryID(e)})
		}
		return cmd(Like{Entry: EntryID(e)})
	case "vote":
		return cmd(VoteSkip{})
	case "mode":
		mode, err := lookup(playModes, "播放模式", args[1])
		if err != nil {
			return nil, err
		}
		return cmd(SetPlayMode{Mode: mode})
	case "chat":
		return cmd(Chat{Text: strings.Join(args[1:], " ")})
	case "sync":
		return cmd(Sync{})
	case "prepared":
		tr, err := parseTrack(args[0])
		if err != nil {
			return nil, err
		}
		d, err := time.ParseDuration(args[1])
		if err != nil {
			return nil, fmt.Errorf("时长不合法：%w", err)
		}
		return Prepared{Track: tr, Meta: track.Meta{Title: tr.ID}, Result: ready(d)}, nil
	case "failed":
		tr, err := parseTrack(args[0])
		return PrepareFailed{Track: tr, Reason: strings.Join(args[1:], " ")}, err
	case "recommended":
		token, err := parseNumber(args[0])
		if err != nil {
			return nil, err
		}
		tr, err := parseTrack(args[1])
		return Recommended{Token: Token(token), Track: tr, Meta: track.Meta{Title: tr.ID}}, err
	default:
		token, err := parseNumber(args[0])
		return Timer{Token: Token(token)}, err
	}
}

func lookup[V any](m map[string]V, what, key string) (V, error) {
	v, ok := m[key]
	if !ok {
		return v, fmt.Errorf("未知的%s：%q", what, key)
	}
	return v, nil
}

func parseTrack(s string) (track.Track, error) {
	source, id, ok := strings.Cut(s, ":")
	if !ok || source == "" || id == "" {
		return track.Track{}, fmt.Errorf("曲目应写成 来源:ID：%q", s)
	}
	return track.Track{Source: track.Source(source), ID: id}, nil
}

func parseNumber(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("不是数字：%q", s)
	}
	return n, nil
}

func formatEffects(effects []Effect) string {
	var b strings.Builder
	for _, e := range effects {
		b.WriteString(formatEffect(e))
		b.WriteByte('\n')
	}
	return b.String()
}

func formatEffect(e Effect) string {
	switch e := e.(type) {
	case Persist:
		s := e.Snapshot
		return fmt.Sprintf("persist mode=%s entrySeq=%d current=%s pending=%s waiting=%s",
			modeNames[s.Mode], s.EntrySeq, formatPlaying(s.Current), formatEntryPtr(s.Pending), formatEntries(s.Waiting))
	case RecordPlay:
		return fmt.Sprintf("record %s %s-%s", formatEntry(e.Entry), offset(e.StartedAt), offset(e.EndedAt))
	case Emit:
		return fmt.Sprintf("emit %d.%d %s", e.Term, e.Seq, formatEvent(e.Event))
	case Close:
		return "close"
	case Reply:
		return fmt.Sprintf("reply %s %s", e.RequestID, formatResult(e))
	case Prepare:
		return "prepare " + formatTrack(e.Track)
	case Recommend:
		return fmt.Sprintf("recommend %d", e.Token)
	case Schedule:
		return fmt.Sprintf("schedule %d %s", e.Token, offset(e.At))
	}
	panic(fmt.Sprintf("未知的 effect：%#v", e))
}

func formatEvent(e Event) string {
	switch e := e.(type) {
	case MembersChanged:
		parts := make([]string, len(e.Sessions))
		for i, s := range e.Sessions {
			parts[i] = string(s.ID) + "=" + s.Member.ID
		}
		return "members [" + strings.Join(parts, " ") + "]"
	case QueueChanged:
		return fmt.Sprintf("queue pending=%s waiting=%s", formatEntryPtr(e.Pending), formatEntries(e.Waiting))
	case PlaybackChanged:
		return "playback " + formatPlaying(e.Current)
	case ModeChanged:
		return "mode " + modeNames[e.Mode]
	case InfoChanged:
		return fmt.Sprintf("info %s | %s", e.Name, e.Description)
	case ChatPosted:
		return fmt.Sprintf("chat %s %s", e.Member.ID, e.Text)
	case Activity:
		s := fmt.Sprintf("activity %s %s %s", activityNames[e.Kind], memberID(e.Member), formatEntry(e.Entry))
		switch e.Kind {
		case ActivityVoted, ActivitySkipped:
			s += fmt.Sprintf(" %d/%d", e.Votes, e.Required)
		case ActivityFailed:
			s += " " + e.Reason
		}
		return s
	}
	panic(fmt.Sprintf("未知的事件：%#v", e))
}

func formatResult(r Reply) string {
	if r.Err != nil {
		return fmt.Sprintf("error %s %s", errorNames[r.Err.Kind], r.Err.Message)
	}
	switch res := r.Result.(type) {
	case Done:
		return "done"
	case EnqueueResult:
		return "enqueued " + formatEntry(res.Entry)
	case LikeResult:
		return fmt.Sprintf("liked %d", res.Likes)
	case VoteResult:
		s := fmt.Sprintf("voted %d/%d", res.Votes, res.Required)
		if res.Skipped {
			s += " skipped"
		}
		return s
	case SyncResult:
		return fmt.Sprintf("sync term=%d seq=%d sessions=%d", res.View.Term, res.View.Seq, len(res.View.Sessions))
	}
	panic(fmt.Sprintf("未知的回复：%#v", r))
}

func formatEntry(e Entry) string {
	flags := []string{formatTrack(e.Track)}
	if e.Ready {
		flags = append(flags, "ready")
	}
	if e.Likes > 0 {
		flags = append(flags, fmt.Sprintf("likes=%d", e.Likes))
	}
	if e.Round > 0 {
		flags = append(flags, fmt.Sprintf("round=%d", e.Round))
	}
	if e.Auto {
		flags = append(flags, "auto")
	}
	return fmt.Sprintf("#%d(%s)", e.ID, strings.Join(flags, ","))
}

func formatEntryPtr(e *Entry) string {
	if e == nil {
		return "-"
	}
	return formatEntry(*e)
}

func formatEntries(entries []Entry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = formatEntry(e)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func formatPlaying(p *NowPlaying) string {
	if p == nil {
		return "-"
	}
	return formatEntry(p.Entry) + "@" + offset(p.StartedAt)
}

func formatTrack(t track.Track) string { return string(t.Source) + ":" + t.ID }

func offset(t time.Time) string { return t.Sub(t0).String() }

func memberID(m member.Member) string {
	if m.ID == "" {
		return "-"
	}
	return m.ID
}

var modeNames = map[PlayMode]string{Sequential: "sequential", Random: "random", Rotation: "rotation"}

var activityNames = map[ActivityKind]string{
	ActivityAdded:   "added",
	ActivityRemoved: "removed",
	ActivityLiked:   "liked",
	ActivityVoted:   "voted",
	ActivitySkipped: "skipped",
	ActivityFailed:  "failed",
}

var errorNames = map[ErrorKind]string{
	NotFound:       "not-found",
	Forbidden:      "forbidden",
	Conflict:       "conflict",
	QueueFull:      "queue-full",
	RateLimited:    "rate-limited",
	NothingPlaying: "nothing-playing",
}
