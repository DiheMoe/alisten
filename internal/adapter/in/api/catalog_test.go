package api

import (
	"context"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type noTracks struct{}

func (noTracks) PreparedTrack(context.Context, track.Track) (port.PreparedTrack, bool, error) {
	return port.PreparedTrack{}, false, nil
}

func (noTracks) SavePreparedTrack(context.Context, track.Track, port.PreparedTrack) error {
	return nil
}

type history struct{}

func (history) AutoDJSeeds(context.Context, string, bool) ([]track.Track, error) {
	return []track.Track{sunnyTrack}, nil
}

func (history) AutoDJExclusions(context.Context, string) ([]track.Track, error) {
	return nil, nil
}

type quota struct {
	mu   sync.Mutex
	left int
}

func (q *quota) TakeSearch(context.Context, string, int, time.Duration) (time.Duration, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.left == 0 {
		return time.Minute, nil
	}
	q.left--
	return 0, nil
}

var rain = port.Song{
	Track: track.Track{Source: track.NetEase, ID: "185811"},
	Meta:  track.Meta{Title: "七里香", Artist: "周杰伦", Album: "七里香", PageURL: "https://music.163.com/#/song?id=185811", Duration: 299 * time.Second},
}

func newCatalog(rooms port.RoomDirectory, left int) *catalog.Service {
	netease := porttest.NewSource(track.NetEase)
	netease.Add(sunnyTrack.ID, sunnyEntry.Meta, "", nil)
	netease.Add(rain.Track.ID, rain.Meta, "", nil)
	netease.SetSimilar(sunnyTrack.ID, rain.Track.ID)
	netease.AddPlaylist(port.Playlist{ID: "p1", Name: "周杰伦精选", Creator: "alice", TrackCount: 2, PlayCount: 9}, sunnyTrack.ID, rain.Track.ID)
	bilibili := porttest.NewBilibili()
	return catalog.New(netease, porttest.NewSource(track.QQ), bilibili, bilibili, noTracks{}, history{}, rooms, &quota{left: left})
}
