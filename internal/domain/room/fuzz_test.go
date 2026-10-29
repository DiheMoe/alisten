package room

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	// 房间换到其他实例运行：以下一个任期从存储中的状态恢复。
	fuzzRestore = 17
	// 准备结果写进了曲目表，却没有送到房间（房间没有 owner，或者通知丢失）。
	fuzzStored = 18
	// 曲目表中的结果不能再用：比如链接曲目的音频已经不在对象存储中。
	fuzzDropped = 19
	fuzzOps     = 20
)

// ops 中每两个字节是一步：操作和它的参数。
func FuzzInvariants(f *testing.F) {
	f.Add(false, []byte{0, 0, 2, 1, 2, 2, 7, 1, 7, 2, 4, 2, 5, 0, 9, 3})
	f.Add(true, []byte{6, 1, 2, 3, 2, 4, 2, 5, 7, 3, 8, 4, 3, 2, 9, 9, 11, 1})
	f.Add(false, []byte{0, 0, 2, 4, 7, 10, 11, 1})
	f.Add(true, []byte{2, 0, 7, 0, 2, 1, 10, 0})
	f.Add(true, []byte{0, 0, 2, 1, 7, 1, 2, 2, 17, 0, 12, 1, 13, 0, 15, 0, 16, 0, 4, 2, 3, 2, 7, 2, 9, 0, 14, 0})
	f.Add(false, []byte{0, 1, 2, 0, 2, 1, 2, 2, 7, 0, 17, 3, 9, 0, 4, 1, 3, 1, 1, 1, 17, 0, 10, 0})
	// 正在播放的歌放完之后才恢复，等待队列中还有歌。
	f.Add(true, []byte{0, 0, 2, 0, 7, 0, 2, 1, 7, 1, 16, 15, 16, 15, 16, 15, 16, 15, 17, 0})
	// 恢复时待播和等待队列中准备中的歌重新准备。
	f.Add(false, []byte{2, 0, 17, 0})
	f.Add(true, []byte{2, 0, 7, 0, 2, 1, 17, 0})
	// 待播准备好的结果在房间没有 owner 时写进了曲目表：恢复时待播已就绪，立即开播。
	f.Add(true, []byte{2, 0, 18, 0, 17, 0})
	// 等待队列中已就绪歌曲的结果不能再用：恢复时重新准备。
	f.Add(true, []byte{2, 0, 7, 0, 2, 1, 7, 1, 19, 1, 17, 0})
	f.Fuzz(func(t *testing.T, persistent bool, ops []byte) {
		kind := Ephemeral
		if persistent {
			kind = Persistent
		}
		term := uint32(1)
		r, _ := Restore(at(0), term, Snapshot{Info: Info{ID: "r1", Kind: kind}, Mode: Sequential})
		table := map[track.Track]track.Prepared{}
		var seq uint64
		now := time.Duration(0)
		for i := 0; i+1 < len(ops); i += 2 {
			op, arg := ops[i], ops[i+1]
			now += time.Duration(arg%16) * time.Second
			env := Env{Now: t0.Add(now), Seed: uint64(i)}
			before := r.snapshot()
			var effects []Effect
			switch op % fuzzOps {
			case fuzzRestore:
				// 每次状态变化都已落盘，所以落盘的状态就是 before；会话、投票和定时器不落盘，随旧的 Room 一起丢弃。
				term++
				seq = 0
				before = stored(before, table)
				r, effects = Restore(env, term, before)
				checkRestorePrepares(t, r, effects)
			case fuzzStored:
				table[fuzzTrack(arg)] = ready(time.Duration(arg%5+1) * time.Minute)
				continue
			case fuzzDropped:
				delete(table, fuzzTrack(arg))
				continue
			default:
				in := fuzzInput(r, op, arg)
				if p, ok := in.(Prepared); ok {
					table[p.Track] = p.Result
				}
				effects = r.Handle(env, in)
			}
			// 房间关闭后就被丢弃，不会再收到输入；关闭时结束了正在播放的歌，但不再开播下一首。
			closed := len(find[Close](effects)) != 0
			checkInvariants(t, r, before, effects, term, &seq, closed)
			if closed {
				return
			}
		}
	})
}

func fuzzTrack(arg byte) track.Track {
	return netease(string(rune('a' + arg%6)))
}

// 存储接管房间时按曲目表重算待播和等待队列中的条目是否就绪；正在播放的歌已经开播，保持原样。
func stored(s Snapshot, table map[track.Track]track.Prepared) Snapshot {
	restore := func(e Entry) Entry {
		e.Prep, e.Ready = table[e.Track]
		return e
	}
	s.Waiting = slices.Clone(s.Waiting)
	for i, e := range s.Waiting {
		s.Waiting[i] = restore(e)
	}
	if s.Pending != nil {
		e := restore(*s.Pending)
		s.Pending = &e
	}
	return s
}

func checkRestorePrepares(t *testing.T, r *Room, effects []Effect) {
	t.Helper()
	want := map[track.Track]bool{}
	if r.pending != nil && !r.pending.Ready {
		want[r.pending.Track] = true
	}
	for _, e := range r.waiting {
		if !e.Ready {
			want[e.Track] = true
		}
	}
	got := map[track.Track]bool{}
	for _, p := range find[Prepare](effects) {
		got[p.Track] = true
	}
	if !maps.Equal(got, want) {
		t.Fatalf("恢复时发出 Prepare 的曲目 = %v，期望准备中的条目 %v", got, want)
	}
}

// 不能用 sameSnapshot 判断持久状态是否变化：要检查的正是它。
func persistentStateChanged(a, b Snapshot) bool {
	if len(a.Waiting) == 0 && len(b.Waiting) == 0 {
		a.Waiting, b.Waiting = nil, nil
	}
	return !reflect.DeepEqual(a, b)
}

func fuzzInput(r *Room, op, arg byte) Input {
	members := []member.Member{alice, bob, carol}
	m := members[int(arg)%len(members)]
	tr := fuzzTrack(arg)
	session := SessionID(fmt.Sprint("s", arg%4))
	switch op % fuzzOps {
	case 0:
		return Join{Session: session, Member: m}
	case 1:
		return Leave{Session: session}
	case 2:
		return command(m, Enqueue{Track: tr})
	case 3:
		return command(m, Remove{Entry: fuzzEntry(r, arg)})
	case 4:
		return command(m, Like{Entry: fuzzEntry(r, arg)})
	case 5:
		return command(m, VoteSkip{})
	case 6:
		return command(m, SetPlayMode{Mode: PlayMode(arg%3) + 1})
	case 7:
		return Prepared{Track: tr, Result: ready(time.Duration(arg%5+1) * time.Minute)}
	case 8:
		return PrepareFailed{Track: tr, Reason: "失败"}
	case 9:
		return Timer{Token: r.endTimer}
	case 10:
		return Timer{Token: r.closeTimer}
	case 11:
		return Recommended{Token: r.recommendation, Track: tr}
	case 12:
		return UpdateInfo{RequestID: "q", Name: fmt.Sprint("房间", arg%3), Description: "介绍"}
	case 13:
		return MemberUpdated{Member: member.Member{ID: m.ID, Name: fmt.Sprint(m.Name, arg%2)}}
	case 14:
		return Delete{RequestID: "q"}
	case 15:
		return command(m, Chat{Text: "你好"})
	default:
		return command(m, Sync{})
	}
}

// 多数时候选中等待队列中的条目，也会选中待播、正在播放和不存在的条目：删除和点赞只接受等待队列中的条目。
func fuzzEntry(r *Room, arg byte) EntryID {
	candidates := ids(r.waiting)
	if r.pending != nil {
		candidates = append(candidates, r.pending.ID)
	}
	if r.current != nil {
		candidates = append(candidates, r.current.Entry.ID)
	}
	candidates = append(candidates, r.entrySeq+1)
	return candidates[int(arg)%len(candidates)]
}

func checkInvariants(t *testing.T, r *Room, before Snapshot, effects []Effect, term uint32, seq *uint64, closed bool) {
	t.Helper()
	entries := slices.Clone(r.waiting)
	if r.pending != nil {
		entries = append(entries, *r.pending)
	}
	ids := map[EntryID]bool{}
	tracks := map[track.Track]bool{}
	for _, e := range entries {
		if ids[e.ID] || tracks[e.Track] {
			t.Fatalf("待播和等待队列中有重复的条目或曲目：%#v", entries)
		}
		ids[e.ID], tracks[e.Track] = true, true
	}
	if r.current != nil && ids[r.current.Entry.ID] {
		t.Fatalf("正在播放的条目 ID 与队列中的重复：%d", r.current.Entry.ID)
	}
	if r.current != nil && r.pending != nil {
		t.Fatal("有待播时不应有正在播放的歌")
	}
	if !closed && r.current == nil && r.pending == nil && len(r.waiting) != 0 {
		t.Fatalf("空闲时等待队列不应有歌：%#v", r.waiting)
	}
	if r.pending != nil && r.pending.Ready {
		t.Fatalf("已就绪的待播应立即开播：%#v", r.pending)
	}
	if !slices.IsSortedFunc(r.waiting, compareWaiting(r.mode)) {
		t.Fatalf("等待队列没有按播放模式的排序规则排列：%#v", r.waiting)
	}
	if persistentStateChanged(before, r.snapshot()) {
		if !persistsFirst(effects) {
			t.Fatalf("持久状态变化时 Persist 应排在最前：%#v", effects)
		}
	} else if len(find[Persist](effects)) != 0 {
		t.Fatalf("持久状态没变却落盘：%#v", effects)
	}
	for _, e := range find[Emit](effects) {
		if e.Term != term || e.Seq != *seq+1 {
			t.Fatalf("任期 %d 中 seq %d 之后是任期 %d 的 %d", term, *seq, e.Term, e.Seq)
		}
		*seq = e.Seq
	}
}
