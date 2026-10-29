package porttest

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type Preparer struct {
	mu    sync.Mutex
	songs []song
}

type song struct {
	track    track.Track
	meta     track.Meta
	prepared track.Prepared
}

var _ port.Preparer = (*Preparer)(nil)

var ErrUnknownTrack = errors.New("音源中没有这首歌")

func NewPreparer() *Preparer {
	return &Preparer{}
}

func (p *Preparer) Add(t track.Track, m track.Meta, result track.Prepared) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.songs = append(p.songs, song{track: t, meta: m, prepared: result})
}

func (p *Preparer) Prepare(_ context.Context, t track.Track) (track.Meta, track.Prepared, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.IndexFunc(p.songs, func(s song) bool { return s.track == t })
	if i < 0 {
		return track.Meta{}, track.Prepared{}, ErrUnknownTrack
	}
	return p.songs[i].meta, p.songs[i].prepared, nil
}

func (p *Preparer) Finished(port.PrepareOutcome) {}
