package porttest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type Source struct {
	source track.Source

	mu        sync.Mutex
	songs     []sourceSong
	playlists []sourcePlaylist
	similar   map[string][]string
	failure   error
}

type sourceSong struct {
	id     string
	meta   track.Meta
	lyric  string
	audio  []byte
	reject string
}

type sourcePlaylist struct {
	playlist port.Playlist
	ids      []string
}

var (
	_ port.NetEaseSource = (*Source)(nil)
	_ port.PlayURLs      = (*Source)(nil)
)

var ErrUnknownSong = errors.New("音源中没有这首歌")

func NewSource(source track.Source) *Source {
	return &Source{source: source, similar: map[string][]string{}}
}

func (s *Source) Add(id string, m track.Meta, lyric string, audio []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.songs = append(s.songs, sourceSong{id: id, meta: m, lyric: lyric, audio: audio})
}

func (s *Source) Reject(id, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.songs {
		if s.songs[i].id == id {
			s.songs[i].reject = reason
		}
	}
}

// ids 是歌单中的歌，必须已经登记过。
func (s *Source) AddPlaylist(p port.Playlist, ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playlists = append(s.playlists, sourcePlaylist{playlist: p, ids: ids})
}

// ids 是 id 的相似歌曲，必须已经登记过。
func (s *Source) SetSimilar(id string, ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.similar[id] = ids
}

func (s *Source) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = err
}

func (s *Source) SearchTracks(_ context.Context, keyword string) ([]port.Song, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return nil, s.failure
	}
	var songs []port.Song
	for _, song := range s.songs {
		if strings.Contains(song.meta.Title, keyword) || strings.Contains(song.meta.Artist, keyword) {
			songs = append(songs, s.song(song))
		}
	}
	return songs, nil
}

func (s *Source) SearchPlaylists(_ context.Context, keyword string) ([]port.Playlist, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return nil, s.failure
	}
	var playlists []port.Playlist
	for _, p := range s.playlists {
		if strings.Contains(p.playlist.Name, keyword) {
			playlists = append(playlists, p.playlist)
		}
	}
	return playlists, nil
}

func (s *Source) PlaylistTracks(_ context.Context, playlistID string) ([]port.Song, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return nil, s.failure
	}
	i := slices.IndexFunc(s.playlists, func(p sourcePlaylist) bool { return p.playlist.ID == playlistID })
	if i < 0 {
		return nil, errors.New("音源中没有这个歌单")
	}
	return s.lookup(s.playlists[i].ids), nil
}

func (s *Source) SimilarTracks(_ context.Context, id string) ([]port.Song, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return nil, s.failure
	}
	return s.lookup(s.similar[id]), nil
}

func (s *Source) TrackMeta(_ context.Context, id string) (track.Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return track.Meta{}, s.failure
	}
	song, ok := s.find(id)
	if !ok {
		return track.Meta{}, &port.RejectedError{Err: ErrUnknownSong}
	}
	return song.meta, nil
}

func (s *Source) Resolve(_ context.Context, id string) (port.Resolved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return port.Resolved{}, s.failure
	}
	song, ok := s.find(id)
	switch {
	case !ok:
		return port.Resolved{}, &port.RejectedError{Err: ErrUnknownSong}
	case song.reject != "":
		return port.Resolved{}, &port.RejectedError{Err: errors.New(song.reject)}
	}
	return port.Resolved{Meta: song.meta, Lyric: song.lyric, Audio: io.NopCloser(bytes.NewReader(song.audio))}, nil
}

func (s *Source) PlayURL(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return "", s.failure
	}
	song, ok := s.find(id)
	switch {
	case !ok:
		return "", &port.RejectedError{Err: ErrUnknownSong}
	case song.reject != "":
		return "", &port.RejectedError{Err: errors.New(song.reject)}
	}
	return "http://" + string(s.source) + ".example/" + id + ".mp3", nil
}

// 调用方持有 s.mu。
func (s *Source) find(id string) (sourceSong, bool) {
	i := slices.IndexFunc(s.songs, func(song sourceSong) bool { return song.id == id })
	if i < 0 {
		return sourceSong{}, false
	}
	return s.songs[i], true
}

// 调用方持有 s.mu。
func (s *Source) lookup(ids []string) []port.Song {
	songs := make([]port.Song, 0, len(ids))
	for _, id := range ids {
		if song, ok := s.find(id); ok {
			songs = append(songs, s.song(song))
		}
	}
	return songs
}

func (s *Source) song(song sourceSong) port.Song {
	return port.Song{Track: track.Track{Source: s.source, ID: song.id}, Meta: song.meta}
}

// 静音的响度分析得到的增益正好是 1。
func SilentWAV(d time.Duration) []byte {
	const rate = 8000
	frames := int(d * rate / time.Second)
	b := []byte("RIFF")
	b = binary.LittleEndian.AppendUint32(b, uint32(36+frames))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16) // fmt 块的长度
	b = binary.LittleEndian.AppendUint16(b, 1)  // PCM
	b = binary.LittleEndian.AppendUint16(b, 1)  // 声道数
	b = binary.LittleEndian.AppendUint32(b, rate)
	b = binary.LittleEndian.AppendUint32(b, rate) // 每秒的字节数
	b = binary.LittleEndian.AppendUint16(b, 1)    // 每帧的字节数
	b = binary.LittleEndian.AppendUint16(b, 8)    // 位深
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(frames))
	// 8 位 PCM 是无符号的，静音是 128。
	return append(b, bytes.Repeat([]byte{128}, frames)...)
}

// 1 kHz 的正弦波，峰值是满幅的 amplitude 倍：响度分析得到的增益不是 1。
func ToneWAV(d time.Duration, amplitude float64) []byte {
	const rate, channels = 44100, 2
	frames := int(d * rate / time.Second)
	size := frames * channels * 2
	b := []byte("RIFF")
	b = binary.LittleEndian.AppendUint32(b, uint32(36+size))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16) // fmt 块的长度
	b = binary.LittleEndian.AppendUint16(b, 1)  // PCM
	b = binary.LittleEndian.AppendUint16(b, channels)
	b = binary.LittleEndian.AppendUint32(b, rate)
	b = binary.LittleEndian.AppendUint32(b, rate*channels*2) // 每秒的字节数
	b = binary.LittleEndian.AppendUint16(b, channels*2)      // 每帧的字节数
	b = binary.LittleEndian.AppendUint16(b, 16)              // 位深
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(size))
	for i := range frames {
		v := int16(math.Round(amplitude * math.MaxInt16 * math.Sin(2*math.Pi*1000*float64(i)/rate)))
		for range channels {
			b = binary.LittleEndian.AppendUint16(b, uint16(v))
		}
	}
	return b
}
