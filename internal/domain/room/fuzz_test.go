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
	// 房间换到其他实例运行，以下一个任期从存储中的状态恢复。
	fuzzRestore = 17
	// 准备结果写进了曲目表，却没有送到房间（房间没有 owner，或者通知丢失）。
	fuzzStored = 18
	// 曲目表中的结果不能再用了，比如链接曲目的音频已经不在对象存储中。
	fuzzDropped = 19
	// 宽限期的定时器在 arg 为偶数时按设定的时刻触发，这样宽限期结束的那一刻也能测到。arg 为奇数时不调时间，
	// 模拟定时器早于设定的时刻送到。定时器按单调时钟等待，恢复出来的点歌时间却只有墙上时间，系统时钟被往回调时就会这样。
	fuzzGrace = 20
	fuzzOps   = 21
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
	// 待播准备好的结果在房间没有 owner 时写进了曲目表，所以恢复时待播已就绪，立即开播。
	f.Add(true, []byte{2, 0, 18, 0, 17, 0})
	// 等待队列中已就绪歌曲的结果不能再用了，恢复时要重新准备。
	f.Add(true, []byte{2, 0, 7, 0, 2, 1, 7, 1, 19, 1, 17, 0})
	// 待播过了宽限期还没准备好，先放后面准备好的歌。
	f.Add(false, []byte{2, 0, 2, 1, 7, 1, 20, 0, 7, 0})
	// 有歌在播时，排在前面的歌过了宽限期还没准备好，下一首换成后面准备好的歌。
	f.Add(false, []byte{2, 0, 7, 0, 2, 1, 2, 2, 7, 2, 20, 0})
	// 宽限期的定时器提前送到，要重新设定时器。
	f.Add(false, []byte{2, 0, 2, 1, 7, 1, 20, 1})
	// 删除待播。
	f.Add(true, []byte{2, 0, 2, 1, 3, 1})
	// 4 个人在线，其中一人开了两个连接，投了 1 票。关掉一个连接时门槛不变，再有一个人离开，门槛降到 1，要切歌。
	f.Add(true, []byte{0, 0, 0, 1, 0, 2, 0, 3, 0, 4, 2, 0, 7, 0, 5, 0, 1, 4, 1, 1})
	// 随机模式切歌时，抽好的歌过了宽限期还没准备好，改放唯一准备好的那首。选完之后等待队列里没有准备好的歌了，抽好的歌看起来又占着位置。
	f.Add(false, []byte{2, 1, 7, 1, 2, 2, 6, 1, 2, 0, 7, 0, 5, 14})
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
			if op%fuzzOps == fuzzGrace && arg%2 == 0 && r.graceAt.After(t0.Add(now)) {
				now = r.graceAt.Sub(t0)
			}
			env := Env{Now: t0.Add(now), Seed: uint64(i)}
			before := r.snapshot()
			beforeDrawn, beforeNext := r.next, r.upNext()
			var effects []Effect
			switch op % fuzzOps {
			case fuzzRestore:
				// 每次状态变化都已落盘，所以落盘的状态就是 before。会话、投票和定时器不落盘，随旧的 Room 一起丢弃。
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
			// 房间关闭后就被丢弃，不会再收到输入。关闭时结束了正在播放的歌，但不再开播下一首。
			closed := len(find[Close](effects)) != 0
			checkInvariants(t, r, before, effects, term, &seq, closed)
			checkNext(t, r, before, beforeDrawn, beforeNext, op%fuzzOps == fuzzRestore, effects)
			if closed {
				return
			}
		}
	})
}

// 下一首要么没有，要么是有歌在播时等待队列里已准备好的歌。随机模式切歌时选中的是上一批抽好的那首，下一首变了要广播。
// 恢复时抽签只在内存中，换了新的 Room，所以不检查和上一批的关系。
func checkNext(t *testing.T, r *Room, before Snapshot, drawn, beforeNext EntryID, restored bool, effects []Effect) {
	t.Helper()
	next := r.upNext()
	if next != 0 {
		i := r.waitingIndex(next)
		if r.current == nil || i < 0 || !r.waiting[i].Ready {
			t.Fatalf("下一首 #%d 应是有歌在播时等待队列里已准备好的歌：current=%#v waiting=%#v", next, r.current, r.waiting)
		}
	}
	if r.mode != Random && r.next != 0 {
		t.Fatalf("非随机模式不应留着抽好的下一首：%d", r.next)
	}
	if r.mode == Random && r.current != nil && len(r.waiting) > 0 && r.waitingIndex(r.next) < 0 {
		t.Fatalf("随机模式有歌在播时应抽好下一首：next=%d waiting=%#v", r.next, r.waiting)
	}
	if restored {
		return
	}
	if before.Mode == Random && drawn != 0 {
		// 抽好的歌在选歌时不占着位置，才能换成别的歌。
		if selected, ok := selectedFrom(before.Waiting, r); ok && selected != drawn {
			if i := r.waitingIndex(drawn); i >= 0 && heldWhenChosen(r, r.waiting[i], selected) {
				t.Fatalf("随机模式切歌选中了 #%d，抽好的 #%d 还在等待队列里", selected, drawn)
			}
		}
	}
	if next != beforeNext {
		q := events[QueueChanged](effects)
		if len(q) == 0 || q[len(q)-1].Next != next {
			t.Fatalf("下一首从 #%d 变成 #%d，没有广播：%#v", beforeNext, next, effects)
		}
	}
}

// 选歌时 e 是否占着位置。被选中的歌已经离开等待队列，它开播了的话，说明选歌时等待队列里有准备好的歌，
// 这时过了宽限期还没准备好的歌不占位置，而现在的等待队列里可能已经没有准备好的歌了。
func heldWhenChosen(r *Room, e Entry, selected EntryID) bool {
	if r.current != nil && r.current.Entry.ID == selected {
		return e.Ready || r.now.Before(e.AddedAt.Add(prepareGrace))
	}
	return r.holdsTurn()(e)
}

// 这一批从等待队列里选中的歌，开播了或者成为待播。
func selectedFrom(waiting []Entry, r *Room) (EntryID, bool) {
	was := func(id EntryID) bool { return slices.ContainsFunc(waiting, func(e Entry) bool { return e.ID == id }) }
	if r.current != nil && was(r.current.Entry.ID) {
		return r.current.Entry.ID, true
	}
	if r.pending != nil && was(r.pending.ID) {
		return r.pending.ID, true
	}
	return 0, false
}

func fuzzTrack(arg byte) track.Track {
	return netease(string(rune('a' + arg%6)))
}

// 存储接管房间时按曲目表重算待播和等待队列中的条目是否就绪。正在播放的歌已经开播，保持原样。
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

// 不能用 sameSnapshot 判断持久状态是否变化，因为要检查的正是 sameSnapshot。
func persistentStateChanged(a, b Snapshot) bool {
	if len(a.Waiting) == 0 && len(b.Waiting) == 0 {
		a.Waiting, b.Waiting = nil, nil
	}
	return !reflect.DeepEqual(a, b)
}

func fuzzInput(r *Room, op, arg byte) Input {
	// 4 个人在线时投票门槛是 2，才测得到票数不够的情况。会话数比成员多，同一成员才会有多个连接。
	members := []member.Member{alice, bob, carol, dave}
	m := members[int(arg)%len(members)]
	tr := fuzzTrack(arg)
	session := SessionID(fmt.Sprint("s", arg%5))
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
		name, description := fmt.Sprint("房间", arg%3), "介绍"
		return UpdateInfo{RequestID: "q", Name: &name, Description: &description}
	case 13:
		return MemberUpdated{Member: member.Member{ID: m.ID, Name: fmt.Sprint(m.Name, arg%2)}}
	case 14:
		return Delete{RequestID: "q"}
	case 15:
		return command(m, Chat{Text: "你好"})
	case fuzzGrace:
		return Timer{Token: r.graceTimer}
	default:
		return command(m, Sync{})
	}
}

// 多数时候选中等待队列中的条目，因为点赞只接受这些条目，删除只接受这些条目和待播。待播、正在播放和不存在的条目也会选到。
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
	// 票数一够就切歌，有人离开、门槛变低时也一样。
	if r.current != nil && len(r.votes) >= r.voteThreshold() {
		t.Fatalf("有歌在播时票数应少于门槛：%d/%d", len(r.votes), r.voteThreshold())
	}
	if !closed && r.current == nil && r.pending == nil && len(r.waiting) != 0 {
		t.Fatalf("空闲时等待队列不应有歌：%#v", r.waiting)
	}
	if r.pending != nil && r.pending.Ready {
		t.Fatalf("已就绪的待播应立即开播：%#v", r.pending)
	}
	if !closed && r.pending != nil && !r.holdsTurn()(*r.pending) {
		t.Fatalf("待播过了宽限期还没准备好，等待队列里又有准备好的歌时，应先放准备好的歌：pending=%#v waiting=%#v", r.pending, r.waiting)
	}
	// 没有新的输入时，只有这个定时器能让房间不再等这首歌。
	if e := r.awaited(); !closed && e != nil && !e.Ready && r.anyReady() && (r.graceTimer == 0 || !r.graceAt.Equal(e.AddedAt.Add(prepareGrace))) {
		t.Fatalf("房间在等没准备好的 #%d，又有准备好的歌，宽限期结束时应有定时器：graceAt=%v graceTimer=%d", e.ID, r.graceAt, r.graceTimer)
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
	checkListen(t, r, effects)
	for _, e := range find[Emit](effects) {
		if e.Term != term || e.Seq != *seq+1 {
			t.Fatalf("任期 %d 中 seq %d 之后是任期 %d 的 %d", term, *seq, e.Term, e.Seq)
		}
		*seq = e.Seq
	}
}

// 听歌记录只记正在播放的歌和在线的成员，排在落盘之后、广播之前。
func checkListen(t *testing.T, r *Room, effects []Effect) {
	t.Helper()
	i := slices.IndexFunc(effects, func(e Effect) bool { _, ok := e.(RecordListen); return ok })
	if i < 0 {
		return
	}
	l := effects[i].(RecordListen)
	if len(find[RecordListen](effects)) != 1 {
		t.Fatalf("一批中有多个 RecordListen：%#v", effects)
	}
	if len(find[Emit](effects[:i])) != 0 || len(find[Persist](effects[i:])) != 0 {
		t.Fatalf("RecordListen 应排在 Persist 之后、Emit 之前：%#v", effects)
	}
	if r.current == nil || l.Entry != r.current.Entry || !l.StartedAt.Equal(r.current.StartedAt) {
		t.Fatalf("RecordListen 记的不是正在播放的歌：%#v，正在播放 %#v", l, r.current)
	}
	if len(l.Members) == 0 {
		t.Fatal("RecordListen 没有成员")
	}
	seen := map[string]bool{}
	for _, id := range l.Members {
		if seen[id] || !slices.ContainsFunc(r.sessions, func(s Session) bool { return s.Member.ID == id }) {
			t.Fatalf("RecordListen 的成员重复或不在线：%v，在线 %#v", l.Members, r.sessions)
		}
		seen[id] = true
	}
}
