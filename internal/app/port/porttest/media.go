package porttest

import (
	"cmp"
	"context"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.MediaStore = (*MediaStore)(nil)

type MediaStore struct {
	mu      sync.Mutex
	objects map[string]Object
	failure error
}

type Object struct {
	Data        []byte
	ContentType string
}

func NewMediaStore() *MediaStore {
	return &MediaStore{objects: map[string]Object{}}
}

func (s *MediaStore) Put(_ context.Context, key string, body io.ReadSeeker, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	s.objects[key] = Object{Data: data, ContentType: contentType}
	return nil
}

func (s *MediaStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return false, s.failure
	}
	_, ok := s.objects[key]
	return ok, nil
}

func (s *MediaStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	delete(s.objects, key)
	return nil
}

func (s *MediaStore) Object(key string) (o Object, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok = s.objects[key]
	return o, ok
}

func (s *MediaStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.objects))
}

func (s *MediaStore) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = err
}

var _ port.MediaLibrary = (*MediaLibrary)(nil)

type MediaLibrary struct {
	mu    sync.Mutex
	media map[string]port.StoredMedia
}

func NewMediaLibrary(media ...port.StoredMedia) *MediaLibrary {
	l := &MediaLibrary{media: map[string]port.StoredMedia{}}
	for _, m := range media {
		l.media[m.Key] = m
	}
	return l
}

func (l *MediaLibrary) ListStoredMedia(_ context.Context, q port.MediaQuery) (port.MediaPage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var page port.MediaPage
	var matched []port.StoredMedia
	for _, m := range l.media {
		if strings.Contains(strings.ToLower(m.Title), strings.ToLower(q.Title)) {
			matched = append(matched, m)
			page.TotalBytes += m.Size
		}
	}
	slices.SortFunc(matched, func(a, b port.StoredMedia) int {
		return cmp.Or(compareMedia(q.Sort, a, b), strings.Compare(a.Link, b.Link))
	})
	page.Total = len(matched)
	if q.Page-1 > len(matched)/port.MediaPageSize {
		return page, nil
	}
	start := (q.Page - 1) * port.MediaPageSize
	page.Items = matched[start:min(start+port.MediaPageSize, len(matched))]
	return page, nil
}

func compareMedia(sort port.MediaSort, a, b port.StoredMedia) int {
	switch sort {
	case port.MediaByStored:
		return b.StoredAt.Compare(a.StoredAt)
	case port.MediaByIdle:
		switch {
		case a.LastPlayedAt == nil && b.LastPlayedAt == nil:
			return 0
		case a.LastPlayedAt == nil:
			return -1
		case b.LastPlayedAt == nil:
			return 1
		}
		return a.LastPlayedAt.Compare(*b.LastPlayedAt)
	case port.MediaBySize:
		return cmp.Compare(b.Size, a.Size)
	}
	return 0
}

func (l *MediaLibrary) StoredMedia(_ context.Context, key string) (port.StoredMedia, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.media[key]
	return m, ok, nil
}

func (l *MediaLibrary) DeleteStoredMedia(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.media, key)
	return nil
}
