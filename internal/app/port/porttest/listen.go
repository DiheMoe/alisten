package porttest

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	_ port.ListenRecorder = (*Store)(nil)
	_ port.Listens        = (*Store)(nil)
)

func (s *Store) RecordListen(_ context.Context, memberIDs []string, l port.Listen) error {
	m := l.Meta
	if err := checkText(string(l.Track.Source), l.Track.ID, m.Title, m.Artist, m.Album, m.CoverURL, string(m.CoverKey), m.PageURL); err != nil {
		return err
	}
	// 与 PostgreSQL 存下的精度一致。
	l.Meta.Duration = m.Duration.Truncate(time.Millisecond)
	l.HeardAt = l.HeardAt.Truncate(time.Microsecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range memberIDs {
		if stored := s.member(id); stored != nil && !stored.listensOff {
			s.addListen(id, l)
		}
	}
	return nil
}

// 调用方持有 s.mu。
func (s *Store) addListen(memberID string, l port.Listen) {
	list := s.listens[memberID]
	if i := slices.IndexFunc(list, func(old port.Listen) bool { return old.Track == l.Track }); i >= 0 {
		if list[i].HeardAt.After(l.HeardAt) {
			l.HeardAt = list[i].HeardAt
		}
		list = slices.Delete(list, i, i+1)
	}
	list = append(list, l)
	slices.SortFunc(list, func(a, b port.Listen) int {
		return cmp.Or(b.HeardAt.Compare(a.HeardAt), cmp.Compare(a.Track.Source, b.Track.Source), cmp.Compare(a.Track.ID, b.Track.ID))
	})
	s.listens[memberID] = list[:min(len(list), port.ListenLimit)]
}

func (s *Store) MemberListens(_ context.Context, memberID string) ([]port.Listen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.listens[memberID]), nil
}

func (s *Store) DeleteListen(_ context.Context, memberID string, t track.Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listens[memberID] = slices.DeleteFunc(s.listens[memberID], func(l port.Listen) bool { return l.Track == t })
	return nil
}

func (s *Store) ClearListens(_ context.Context, memberID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.listens, memberID)
	return nil
}

func (s *Store) ListenRecording(_ context.Context, memberID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil {
		return false, port.ErrMemberNotFound
	}
	return !m.listensOff, nil
}

func (s *Store) SetListenRecording(_ context.Context, memberID string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil {
		return port.ErrMemberNotFound
	}
	m.listensOff = !on
	return nil
}
