package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var _ port.RoomSnapshots = (*Store)(nil)

const (
	slotWaiting = "waiting"
	slotPending = "pending"
	slotCurrent = "current"
)

func (s *Store) TakeRoom(ctx context.Context, id string) (uint32, room.Snapshot, error) {
	if !isUUID(id) {
		return 0, room.Snapshot{}, fmt.Errorf("接管房间 %s：%w", id, port.ErrRoomNotFound)
	}
	var term uint32
	var snap room.Snapshot
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.TakeRoom(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return port.ErrRoomNotFound
		}
		if err != nil {
			return err
		}
		entries, err := q.RoomEntries(ctx, row.ID)
		if err != nil {
			return err
		}
		term, snap = uint32(row.Term), snapshotOf(row, entries)
		return nil
	})
	if err != nil {
		return 0, room.Snapshot{}, fmt.Errorf("接管房间 %s：%w", id, err)
	}
	return term, snap, nil
}

func (s *Store) SaveRoom(ctx context.Context, term uint32, snap room.Snapshot, plays []room.RecordPlay) error {
	id := snap.Info.ID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		updated, err := q.SaveRoomState(ctx, query.SaveRoomStateParams{
			ID:          id,
			Term:        int64(term),
			Name:        snap.Info.Name,
			Description: snap.Info.Description,
			PlayMode:    modeName(snap.Mode),
			EntrySeq:    int64(snap.EntrySeq),
		})
		if err != nil {
			return err
		}
		if updated == 0 {
			return port.ErrTermExpired
		}
		if err := q.DeleteRoomEntries(ctx, id); err != nil {
			return err
		}
		if _, err := q.InsertRoomEntries(ctx, entryRows(id, snap)); err != nil {
			return err
		}
		if len(plays) == 0 {
			return nil
		}
		if _, err := q.InsertPlays(ctx, playRows(id, plays)); err != nil {
			return err
		}
		for _, p := range plays {
			err := q.MarkPlayed(ctx, query.MarkPlayedParams{
				Source:    string(p.Entry.Track.Source),
				TrackID:   p.Entry.Track.ID,
				StartedAt: p.StartedAt,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("落盘房间 %s：%w", id, err)
	}
	return nil
}

func (s *Store) DeleteRoom(ctx context.Context, id string, term uint32) error {
	deleted, err := s.q.DeleteRoom(ctx, query.DeleteRoomParams{ID: id, Term: int64(term)})
	if err == nil && deleted == 0 {
		err = port.ErrTermExpired
	}
	if err != nil {
		return fmt.Errorf("删除房间 %s：%w", id, err)
	}
	return nil
}

func snapshotOf(row query.Room, entries []query.RoomEntriesRow) room.Snapshot {
	s := room.Snapshot{
		Info:     roomInfo(row),
		Mode:     modeOf(row.PlayMode),
		EntrySeq: room.EntryID(row.EntrySeq),
	}
	for _, r := range entries {
		e := entryOf(r)
		switch r.Slot {
		case slotCurrent:
			s.Current = &room.NowPlaying{Entry: e, StartedAt: r.StartedAt.UTC()}
		case slotPending:
			s.Pending = &e
		default:
			s.Waiting = append(s.Waiting, e)
		}
	}
	return s
}

func entryOf(r query.RoomEntriesRow) room.Entry {
	e := room.Entry{
		ID:    room.EntryID(r.EntryID),
		Track: track.Track{Source: track.Source(r.Source), ID: r.TrackID},
		Meta: track.Meta{
			Title:    r.Title,
			Artist:   orEmpty(r.Artist),
			Album:    orEmpty(r.Album),
			CoverURL: orEmpty(r.CoverURL),
			CoverKey: track.ObjectKey(orEmpty(r.CoverKey)),
			PageURL:  r.PageURL,
			Duration: time.Duration(r.MetaDurationMs) * time.Millisecond,
		},
		By:      member.Member{ID: orEmpty(r.ByMemberID), Name: orEmpty(r.ByName), AvatarHash: orEmpty(r.ByAvatarHash)},
		Auto:    r.Auto,
		Likes:   int(r.Likes),
		Round:   int(r.Round),
		AddedAt: r.AddedAt.UTC(),
	}
	// 曲目表只保存准备成功的结果，有对应的行就是已就绪。元数据也取自曲目表：在房间没有 owner 时准备好的歌，
	// 条目中仍是点歌时的元数据，比如链接曲目的标题仍是链接本身。
	if r.DurationMs != nil {
		e.Ready = true
		e.Meta, e.Prep = prepared(*r.TrackTitle, orEmpty(r.TrackArtist), orEmpty(r.TrackAlbum), orEmpty(r.TrackCoverURL), orEmpty(r.TrackCoverKey), *r.TrackPageURL, *r.DurationMs, *r.Gain, orEmpty(r.Lyric))
	}
	return e
}

func entryRows(roomID string, s room.Snapshot) []query.InsertRoomEntriesParams {
	rows := make([]query.InsertRoomEntriesParams, 0, len(s.Waiting)+2)
	if s.Current != nil {
		rows = append(rows, entryRow(roomID, slotCurrent, s.Current.Entry, &s.Current.StartedAt))
	}
	if s.Pending != nil {
		rows = append(rows, entryRow(roomID, slotPending, *s.Pending, nil))
	}
	for _, e := range s.Waiting {
		rows = append(rows, entryRow(roomID, slotWaiting, e, nil))
	}
	return rows
}

func entryRow(roomID, slot string, e room.Entry, startedAt *time.Time) query.InsertRoomEntriesParams {
	return query.InsertRoomEntriesParams{
		RoomID:         roomID,
		EntryID:        int64(e.ID),
		Slot:           slot,
		Source:         string(e.Track.Source),
		TrackID:        e.Track.ID,
		Title:          e.Meta.Title,
		Artist:         nullable(e.Meta.Artist),
		Album:          nullable(e.Meta.Album),
		CoverURL:       nullable(e.Meta.CoverURL),
		CoverKey:       nullable(string(e.Meta.CoverKey)),
		PageURL:        e.Meta.PageURL,
		MetaDurationMs: e.Meta.Duration.Milliseconds(),
		ByMemberID:     memberID(e.By),
		ByName:         nullable(e.By.Name),
		ByAvatarHash:   nullable(e.By.AvatarHash),
		Auto:           e.Auto,
		Likes:          int32(e.Likes),
		Round:          int64(e.Round),
		AddedAt:        e.AddedAt,
		StartedAt:      startedAt,
	}
}

func playRows(roomID string, plays []room.RecordPlay) []query.InsertPlaysParams {
	rows := make([]query.InsertPlaysParams, len(plays))
	for i, p := range plays {
		rows[i] = query.InsertPlaysParams{
			RoomID:       roomID,
			Source:       string(p.Entry.Track.Source),
			TrackID:      p.Entry.Track.ID,
			ByMemberID:   memberID(p.Entry.By),
			ByName:       nullable(p.Entry.By.Name),
			ByAvatarHash: nullable(p.Entry.By.AvatarHash),
			Auto:         p.Entry.Auto,
			StartedAt:    p.StartedAt,
			EndedAt:      p.EndedAt,
		}
	}
	return rows
}

func memberID(m member.Member) *string {
	if m.ID == "" {
		return nil
	}
	return &m.ID
}

func modeName(m room.PlayMode) string {
	switch m {
	case room.Random:
		return "random"
	case room.Rotation:
		return "rotation"
	}
	return "sequential"
}

func modeOf(name string) room.PlayMode {
	switch name {
	case "random":
		return room.Random
	case "rotation":
		return room.Rotation
	}
	return room.Sequential
}
