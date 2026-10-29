package porttest

import (
	"fmt"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestSnapshotKeepsRounds(t *testing.T) {
	s := New()
	id, err := s.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	if err != nil {
		t.Fatal(err)
	}
	term, snap, err := s.TakeRoom(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	entry := func(id room.EntryID, round int) room.Entry {
		return room.Entry{ID: id, Track: track.Track{Source: track.NetEase, ID: fmt.Sprint(id)}, Round: round}
	}
	pending := entry(2, 2)
	snap.Mode, snap.EntrySeq = room.Rotation, 3
	snap.Current, snap.Pending, snap.Waiting = &room.NowPlaying{Entry: entry(1, 1)}, &pending, []room.Entry{entry(3, 3)}
	if err := s.SaveRoom(t.Context(), term, snap, nil); err != nil {
		t.Fatal(err)
	}

	_, restored, err := s.TakeRoom(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Mode != room.Rotation || restored.Current.Entry.Round != 1 || restored.Pending.Round != 2 || restored.Waiting[0].Round != 3 {
		t.Fatalf("恢复出的快照没有保留播放模式和轮次：%#v", restored)
	}
}
