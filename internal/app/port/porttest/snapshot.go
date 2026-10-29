package porttest

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var _ port.RoomSnapshots = (*Store)(nil)

type roomState struct {
	term     uint32
	mode     room.PlayMode
	entrySeq room.EntryID
	waiting  []room.Entry
	pending  *room.Entry
	current  *room.NowPlaying
	plays    []room.RecordPlay
}

func (s *Store) TakeRoom(_ context.Context, id string) (uint32, room.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.room(id)
	if !uuidPattern.MatchString(id) || r == nil {
		return 0, room.Snapshot{}, fmt.Errorf("接管房间 %s：%w", id, port.ErrRoomNotFound)
	}
	st := s.states[r.Info.ID]
	if st == nil {
		st = &roomState{mode: room.Sequential}
		s.states[r.Info.ID] = st
	}
	st.term++
	snap := room.Snapshot{Info: r.Info, Mode: st.mode, EntrySeq: st.entrySeq, Waiting: make([]room.Entry, len(st.waiting))}
	for i, e := range st.waiting {
		snap.Waiting[i] = restored(e)
	}
	if st.pending != nil {
		e := restored(*st.pending)
		snap.Pending = &e
	}
	if st.current != nil {
		snap.Current = &room.NowPlaying{Entry: restored(st.current.Entry), StartedAt: st.current.StartedAt}
	}
	return st.term, snap, nil
}

// postgres 适配器按曲目表判断条目是否就绪，这里没有曲目表，恢复出的条目都在准备中。
func restored(e room.Entry) room.Entry {
	e.Ready = false
	e.Prep = track.Prepared{}
	return e
}

func (s *Store) SaveRoom(_ context.Context, term uint32, snap room.Snapshot, plays []room.RecordPlay) error {
	if err := checkText(snapshotTexts(snap)...); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.room(snap.Info.ID)
	st := s.states[snap.Info.ID]
	if r == nil || st == nil || st.term != term {
		return fmt.Errorf("落盘房间 %s：%w", snap.Info.ID, port.ErrTermExpired)
	}
	r.Info.Name, r.Info.Description = snap.Info.Name, snap.Info.Description
	st.mode, st.entrySeq = snap.Mode, snap.EntrySeq
	st.waiting = slices.Clone(snap.Waiting)
	st.pending, st.current = nil, nil
	if snap.Pending != nil {
		e := *snap.Pending
		st.pending = &e
	}
	if snap.Current != nil {
		c := *snap.Current
		st.current = &c
	}
	st.plays = append(st.plays, plays...)
	return nil
}

func (s *Store) DeleteRoom(_ context.Context, id string, term uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[id]
	if s.room(id) == nil || st == nil || st.term != term {
		return fmt.Errorf("删除房间 %s：%w", id, port.ErrTermExpired)
	}
	s.rooms = slices.DeleteFunc(s.rooms, func(r port.RoomRecord) bool { return r.Info.ID == id })
	delete(s.states, id)
	maps.DeleteFunc(s.tickets, func(_ string, t storedTicket) bool { return t.roomID == id })
	return nil
}

func (s *Store) Plays(roomID string) []room.RecordPlay {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.states[roomID]; st != nil {
		return slices.Clone(st.plays)
	}
	return nil
}

func (s *Store) Term(roomID string) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.states[roomID]; st != nil {
		return st.term
	}
	return 0
}

func snapshotTexts(s room.Snapshot) []string {
	texts := []string{s.Info.Name, s.Info.Description}
	entries := slices.Clone(s.Waiting)
	if s.Pending != nil {
		entries = append(entries, *s.Pending)
	}
	if s.Current != nil {
		entries = append(entries, s.Current.Entry)
	}
	for _, e := range entries {
		texts = append(texts, e.Track.ID, e.Meta.Title, e.Meta.Artist, e.Meta.Album, e.Meta.CoverURL, string(e.Meta.CoverKey), e.Meta.PageURL, e.By.Name)
	}
	return texts
}
